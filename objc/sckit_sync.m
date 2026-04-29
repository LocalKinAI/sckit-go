// sckit_sync.m — sync wrappers around ScreenCaptureKit's async APIs.
//
// ScreenCaptureKit is all-async (blocks + delegates). Creating ObjC blocks
// from Go (via purego) is painful. This file wraps the async entry points
// with dispatch_semaphore, exposing plain C ABI functions that Go can call
// via purego.Dlopen + purego.RegisterLibFunc.
//
// Functions exposed:
//   sckit_list_displays       — enumerate displays (one-shot)
//   sckit_capture_display     — one-shot screenshot via SCScreenshotManager
//   sckit_stream_start        — open a persistent SCStream, return handle
//   sckit_stream_next_frame   — block until next frame, copy BGRA bytes out
//   sckit_stream_stop         — tear down SCStream, free handle
//
// All output is 32-bit BGRA, tightly packed (no row padding).
//
// Compile:
//   clang -dynamiclib -arch arm64 -fobjc-arc sckit_sync.m \
//     -framework ScreenCaptureKit -framework CoreMedia -framework CoreVideo \
//     -framework Foundation -framework CoreGraphics \
//     -o libsckit_sync.dylib
//
// Requires macOS 14+ (SCScreenshotManager). Permission: "Screen Recording"
// in System Settings → Privacy & Security (TCC prompt on first use).

#import <ScreenCaptureKit/ScreenCaptureKit.h>
#import <CoreMedia/CoreMedia.h>
#import <CoreVideo/CoreVideo.h>
#import <AppKit/AppKit.h>

// Core Graphics' window-scoped capture paths call CGS internals that
// trip CGS_REQUIRE_INIT when invoked from a CLI binary that never
// connected to WindowServer. NSApplicationLoad forces that connection
// without spinning up a GUI runloop. Idempotent per-process.
static void sckit_ensure_cg_init(void) {
    static dispatch_once_t once;
    dispatch_once(&once, ^{
        NSApplicationLoad();
    });
}

// ─── Error helper ─────────────────────────────────────────────
static void sckit_copy_err(NSError* err, char* out, int cap) {
    if (!out || cap <= 0) return;
    NSString* s = err.localizedDescription ?: @"unknown error";
    const char* c = s.UTF8String ?: "nil";
    strncpy(out, c, cap - 1);
    out[cap - 1] = '\0';
}

static void sckit_copy_str(const char* s, char* out, int cap) {
    if (!out || cap <= 0) return;
    strncpy(out, s, cap - 1);
    out[cap - 1] = '\0';
}

// ─── Shared capture config ────────────────────────────────────
// Mirrors Go's internal cfg struct. Layout:
//
//    offset  size  field
//    ------- ----  ----------------------
//      0      4    width
//      4      4    height
//      8      4    frame_rate
//     12      4    show_cursor
//     16      4    queue_depth
//     20      4    color_space
//     24      4    src_x      — region sourceRect origin (0 = no crop)
//     28      4    src_y
//     32      4    src_w
//     36      4    src_h
//     40      8    exclude_ids   (pointer; 8-byte aligned naturally)
//     48      4    n_exclude
//     52      4    _reserved0
//    ------- ----
//     56 bytes total
//
// color_space: 0=sRGB, 1=DisplayP3, 2=BT.709.
typedef struct {
    int32_t         width;
    int32_t         height;
    int32_t         frame_rate;
    int32_t         show_cursor;
    int32_t         queue_depth;
    int32_t         color_space;
    int32_t         src_x;
    int32_t         src_y;
    int32_t         src_w;
    int32_t         src_h;
    const uint32_t* exclude_ids;
    int32_t         n_exclude;
    int32_t         _reserved0;
} sckit_config_t;  // 56 bytes

// Apply common config to an SCStreamConfiguration. `native_w`/`native_h`
// are the target's natural resolution, used when cfg->width/height are 0.
// When a non-empty source rect is set (src_w > 0 and src_h > 0),
// `sourceRect` is applied and effective width/height use that rect.
static void sckit_apply_config(SCStreamConfiguration* out,
                               const sckit_config_t* cfg,
                               int32_t native_w, int32_t native_h,
                               int32_t* eff_w, int32_t* eff_h) {
    BOOL hasSrc = cfg && cfg->src_w > 0 && cfg->src_h > 0;

    int32_t w, h;
    if (cfg && cfg->width > 0) {
        w = cfg->width;
    } else if (hasSrc) {
        w = cfg->src_w;
    } else {
        w = native_w;
    }
    if (cfg && cfg->height > 0) {
        h = cfg->height;
    } else if (hasSrc) {
        h = cfg->src_h;
    } else {
        h = native_h;
    }

    int32_t fps = (cfg && cfg->frame_rate > 0) ? cfg->frame_rate : 60;
    BOOL cursor = (cfg && cfg->show_cursor == 0) ? NO : YES;
    int32_t qd = (cfg && cfg->queue_depth > 0) ? cfg->queue_depth : 3;

    out.width  = w;
    out.height = h;
    out.pixelFormat = kCVPixelFormatType_32BGRA;
    out.showsCursor = cursor;
    out.minimumFrameInterval = CMTimeMake(1, fps);
    out.queueDepth = qd;

    if (hasSrc) {
        out.sourceRect = CGRectMake(cfg->src_x, cfg->src_y,
                                    cfg->src_w, cfg->src_h);
        out.destinationRect = CGRectMake(0, 0, w, h);
    }

    if (cfg) {
        switch (cfg->color_space) {
        case 1: out.colorSpaceName = kCGColorSpaceDisplayP3; break;
        case 2: out.colorSpaceName = kCGColorSpaceITUR_709; break;
        default: out.colorSpaceName = kCGColorSpaceSRGB; break;
        }
    }

    if (eff_w) *eff_w = w;
    if (eff_h) *eff_h = h;
}

// Resolve a list of window IDs into an NSArray<SCWindow*> for use with
// SCContentFilter's excludingWindows/exceptingWindows parameters. IDs
// that don't match any SCWindow are silently skipped.
static NSArray<SCWindow*>* sckit_resolve_windows(SCShareableContent* content,
                                                 const uint32_t* ids, int32_t n) {
    if (!ids || n <= 0) return @[];
    NSMutableArray<SCWindow*>* out = [NSMutableArray arrayWithCapacity:n];
    for (int32_t i = 0; i < n; i++) {
        uint32_t want = ids[i];
        for (SCWindow* w in content.windows) {
            if (w.windowID == want) {
                [out addObject:w];
                break;
            }
        }
    }
    return out;
}

// ─── 1. List displays ─────────────────────────────────────────
typedef struct {
    uint32_t display_id;
    int32_t  width;
    int32_t  height;
    int32_t  frame_x;
    int32_t  frame_y;
} sckit_display_t;

int sckit_list_displays(sckit_display_t* out, int max, char* err_msg, int err_len) {
    __block int count = -1;
    __block NSError* cap_err = nil;
    dispatch_semaphore_t sem = dispatch_semaphore_create(0);

    [SCShareableContent getShareableContentWithCompletionHandler:
        ^(SCShareableContent* content, NSError* error) {
            if (error) {
                cap_err = error;
                dispatch_semaphore_signal(sem);
                return;
            }
            NSArray<SCDisplay*>* displays = content.displays;
            int n = (int)displays.count;
            if (n > max) n = max;
            for (int i = 0; i < n; i++) {
                SCDisplay* d = displays[i];
                out[i].display_id = d.displayID;
                out[i].width      = (int32_t)d.width;
                out[i].height     = (int32_t)d.height;
                out[i].frame_x    = (int32_t)d.frame.origin.x;
                out[i].frame_y    = (int32_t)d.frame.origin.y;
            }
            count = n;
            dispatch_semaphore_signal(sem);
        }];

    dispatch_semaphore_wait(sem, DISPATCH_TIME_FOREVER);
    if (cap_err) sckit_copy_err(cap_err, err_msg, err_len);
    return count;
}

// ─── 2. One-shot capture ──────────────────────────────────────
// Writes BGRA bytes into `out_pixels` (caller-allocated, >= w*h*4 bytes).
// Sets *out_width / *out_height with the captured dimensions.
// Returns bytes written, or -1 on error.
//
// `cfg` is optional (NULL = all defaults: native resolution, cursor shown,
// sRGB). Stream-only fields (frame_rate, queue_depth) are ignored here.
int sckit_capture_display(uint32_t display_id,
                          const sckit_config_t* cfg,
                          uint8_t* out_pixels, int out_cap,
                          int32_t* out_width, int32_t* out_height,
                          char* err_msg, int err_len) {
    sckit_ensure_cg_init();
    __block int result = -1;
    __block NSError* cap_err = nil;
    dispatch_semaphore_t sem = dispatch_semaphore_create(0);

    [SCShareableContent getShareableContentWithCompletionHandler:
        ^(SCShareableContent* content, NSError* error) {
            if (error) { cap_err = error; dispatch_semaphore_signal(sem); return; }

            SCDisplay* target = nil;
            for (SCDisplay* d in content.displays) {
                if (d.displayID == display_id) { target = d; break; }
            }
            if (!target) {
                cap_err = [NSError errorWithDomain:@"sckit" code:404
                           userInfo:@{NSLocalizedDescriptionKey: @"display not found"}];
                dispatch_semaphore_signal(sem); return;
            }

            NSArray<SCWindow*>* excl = sckit_resolve_windows(content,
                cfg ? cfg->exclude_ids : NULL,
                cfg ? cfg->n_exclude   : 0);
            SCContentFilter* filter = [[SCContentFilter alloc]
                initWithDisplay:target excludingWindows:excl];
            SCStreamConfiguration* config = [[SCStreamConfiguration alloc] init];
            sckit_apply_config(config, cfg,
                               (int32_t)target.width, (int32_t)target.height,
                               NULL, NULL);

            [SCScreenshotManager captureImageWithFilter:filter
                                          configuration:config
                                      completionHandler:^(CGImageRef img, NSError* cerr) {
                    if (cerr || !img) {
                        cap_err = cerr ?: [NSError errorWithDomain:@"sckit" code:1
                                          userInfo:@{NSLocalizedDescriptionKey: @"capture returned nil"}];
                        dispatch_semaphore_signal(sem); return;
                    }
                    size_t w = CGImageGetWidth(img);
                    size_t h = CGImageGetHeight(img);
                    size_t needed = w * h * 4;
                    if (out_width)  *out_width  = (int32_t)w;
                    if (out_height) *out_height = (int32_t)h;
                    if ((int)needed > out_cap) {
                        NSString* d = [NSString stringWithFormat:
                            @"buffer too small: need %zu got %d", needed, out_cap];
                        cap_err = [NSError errorWithDomain:@"sckit" code:2
                                   userInfo:@{NSLocalizedDescriptionKey: d}];
                        dispatch_semaphore_signal(sem); return;
                    }
                    CGColorSpaceRef cs = CGColorSpaceCreateDeviceRGB();
                    CGContextRef ctx = CGBitmapContextCreate(
                        out_pixels, w, h, 8, w * 4, cs,
                        kCGImageAlphaPremultipliedFirst | kCGBitmapByteOrder32Little);
                    CGColorSpaceRelease(cs);
                    if (!ctx) {
                        cap_err = [NSError errorWithDomain:@"sckit" code:3
                                   userInfo:@{NSLocalizedDescriptionKey: @"bitmap context alloc failed"}];
                        dispatch_semaphore_signal(sem); return;
                    }
                    CGContextDrawImage(ctx, CGRectMake(0, 0, w, h), img);
                    CGContextRelease(ctx);
                    result = (int)needed;
                    dispatch_semaphore_signal(sem);
                }];
        }];

    dispatch_semaphore_wait(sem, DISPATCH_TIME_FOREVER);
    if (cap_err) sckit_copy_err(cap_err, err_msg, err_len);
    return result;
}

// ─── 3. Persistent stream ─────────────────────────────────────
// Architecture: SCStream delivers frames to an SCStreamOutput delegate.
// We implement a tiny sink class that stashes the latest CMSampleBuffer
// and signals a semaphore. sckit_stream_next_frame blocks on that
// semaphore, then copies the latest buffer's pixels out to the caller.
//
// "Always return the latest frame, skip stale ones" semantics — correct
// for screen-observation use cases. If consumer is slow, frames are dropped
// on the ObjC side by overwriting the slot.

@interface SCKitFrameSink : NSObject <SCStreamOutput, SCStreamDelegate>
@property (nonatomic, assign) CMSampleBufferRef latestBuffer;  // +1 retained when set
@property (nonatomic, strong) dispatch_semaphore_t frameSem;
@property (nonatomic, assign) BOOL hasPending;
@end

@implementation SCKitFrameSink
- (instancetype)init {
    if ((self = [super init])) {
        _frameSem = dispatch_semaphore_create(0);
        _latestBuffer = NULL;
        _hasPending = NO;
    }
    return self;
}

- (void)stream:(SCStream*)stream
    didOutputSampleBuffer:(CMSampleBufferRef)sampleBuffer
    ofType:(SCStreamOutputType)type {
    if (type != SCStreamOutputTypeScreen) return;
    if (!CMSampleBufferIsValid(sampleBuffer)) return;

    // SCStream delivers sample buffers with a status attachment:
    //   Complete / Started — buffer carries a real CVPixelBuffer
    //   Idle / Blank / Suspended / Stopped — no pixel data, marker only
    //
    // On a static screen most frames are Idle. Consumers expect every
    // NextFrame call to return pixels at the configured rate, so we
    // re-deliver the last-known-good buffer on Idle and only refresh
    // the slot on Complete/Started. This preserves the "return pixels
    // at the stream's frame rate" contract without spraying
    // no-image-buffer errors.
    BOOL isContent = YES;
    CFArrayRef attachments = CMSampleBufferGetSampleAttachmentsArray(sampleBuffer, false);
    if (attachments && CFArrayGetCount(attachments) > 0) {
        CFDictionaryRef attach = (CFDictionaryRef)CFArrayGetValueAtIndex(attachments, 0);
        CFTypeRef statusRaw = CFDictionaryGetValue(attach,
            (__bridge CFStringRef)SCStreamFrameInfoStatus);
        if (statusRaw) {
            int statusVal = ((__bridge NSNumber*)statusRaw).intValue;
            // SCFrameStatusComplete = 0, SCFrameStatusStarted = 4.
            if (statusVal != SCFrameStatusComplete &&
                statusVal != SCFrameStatusStarted) {
                isContent = NO;
            }
        }
    }

    BOOL needSignal = NO;
    if (isContent) {
        CFRetain(sampleBuffer);
        @synchronized(self) {
            if (_latestBuffer) CFRelease(_latestBuffer);
            _latestBuffer = sampleBuffer;
            needSignal = !_hasPending;
            _hasPending = YES;
        }
    } else {
        @synchronized(self) {
            // Only signal idle frames if we already have a valid buffer
            // to redeliver; otherwise there's nothing to hand back yet.
            if (_latestBuffer != NULL) {
                needSignal = !_hasPending;
                _hasPending = YES;
            }
        }
    }
    if (needSignal) dispatch_semaphore_signal(_frameSem);
}

- (CMSampleBufferRef)takeBuffer {  // returns +1 retained; caller releases
    CMSampleBufferRef b = NULL;
    @synchronized(self) {
        b = _latestBuffer;
        if (b) CFRetain(b);
        _hasPending = NO;
    }
    return b;
}

- (void)dealloc {
    if (_latestBuffer) CFRelease(_latestBuffer);
}
@end

// Handle is a plain C struct holding retained ObjC pointers via CFBridgingRetain.
typedef struct {
    void* stream;   // SCStream* (retained)
    void* sink;     // SCKitFrameSink* (retained)
    int32_t width;
    int32_t height;
} sckit_stream_t;

void* sckit_stream_start(uint32_t display_id,
                         const sckit_config_t* cfg,
                         char* err_msg, int err_len) {
    sckit_ensure_cg_init();
    __block SCStream* stream = nil;
    __block NSError* cap_err = nil;
    __block int32_t eff_w = 0, eff_h = 0;
    SCKitFrameSink* sink = [[SCKitFrameSink alloc] init];
    dispatch_semaphore_t startSem = dispatch_semaphore_create(0);

    [SCShareableContent getShareableContentWithCompletionHandler:
        ^(SCShareableContent* content, NSError* error) {
            if (error) { cap_err = error; dispatch_semaphore_signal(startSem); return; }
            SCDisplay* target = nil;
            for (SCDisplay* d in content.displays) {
                if (d.displayID == display_id) { target = d; break; }
            }
            if (!target) {
                cap_err = [NSError errorWithDomain:@"sckit" code:404
                           userInfo:@{NSLocalizedDescriptionKey: @"display not found"}];
                dispatch_semaphore_signal(startSem); return;
            }
            NSArray<SCWindow*>* excl = sckit_resolve_windows(content,
                cfg ? cfg->exclude_ids : NULL,
                cfg ? cfg->n_exclude   : 0);
            SCContentFilter* filter = [[SCContentFilter alloc]
                initWithDisplay:target excludingWindows:excl];
            SCStreamConfiguration* config = [[SCStreamConfiguration alloc] init];
            sckit_apply_config(config, cfg,
                               (int32_t)target.width, (int32_t)target.height,
                               &eff_w, &eff_h);

            SCStream* s = [[SCStream alloc] initWithFilter:filter
                                            configuration:config
                                                 delegate:sink];
            NSError* addErr = nil;
            BOOL ok = [s addStreamOutput:sink
                                    type:SCStreamOutputTypeScreen
                      sampleHandlerQueue:dispatch_get_global_queue(QOS_CLASS_USER_INTERACTIVE, 0)
                                   error:&addErr];
            if (!ok) {
                cap_err = addErr;
                dispatch_semaphore_signal(startSem);
                return;
            }
            [s startCaptureWithCompletionHandler:^(NSError* startErr) {
                if (startErr) cap_err = startErr;
                else          stream  = s;
                dispatch_semaphore_signal(startSem);
            }];
        }];

    dispatch_semaphore_wait(startSem, DISPATCH_TIME_FOREVER);
    if (cap_err || !stream) {
        if (cap_err) sckit_copy_err(cap_err, err_msg, err_len);
        return NULL;
    }

    sckit_stream_t* handle = calloc(1, sizeof(sckit_stream_t));
    handle->stream = (void*)CFBridgingRetain(stream);
    handle->sink   = (void*)CFBridgingRetain(sink);
    handle->width  = eff_w;
    handle->height = eff_h;
    return handle;
}

int sckit_stream_dims(void* handle_raw, int32_t* out_width, int32_t* out_height) {
    if (!handle_raw) return -1;
    sckit_stream_t* h = (sckit_stream_t*)handle_raw;
    if (out_width)  *out_width  = h->width;
    if (out_height) *out_height = h->height;
    return 0;
}

// Returns bytes written (w*h*4), -1 on error, -2 on timeout.
int sckit_stream_next_frame(void* handle_raw,
                            uint8_t* out_pixels, int out_cap,
                            int timeout_ms,
                            char* err_msg, int err_len) {
    if (!handle_raw) { sckit_copy_str("nil handle", err_msg, err_len); return -1; }
    sckit_stream_t* handle = (sckit_stream_t*)handle_raw;
    SCKitFrameSink* sink = (__bridge SCKitFrameSink*)handle->sink;

    dispatch_time_t dt = (timeout_ms <= 0)
        ? DISPATCH_TIME_FOREVER
        : dispatch_time(DISPATCH_TIME_NOW, (int64_t)timeout_ms * NSEC_PER_MSEC);
    if (dispatch_semaphore_wait(sink.frameSem, dt) != 0) {
        sckit_copy_str("timeout waiting for frame", err_msg, err_len);
        return -2;
    }

    CMSampleBufferRef buf = [sink takeBuffer];
    if (!buf) {
        sckit_copy_str("no buffer available", err_msg, err_len);
        return -1;
    }

    int ret = -1;
    CVImageBufferRef pix = CMSampleBufferGetImageBuffer(buf);
    if (!pix) {
        sckit_copy_str("sample buffer had no image buffer", err_msg, err_len);
        CFRelease(buf);
        return -1;
    }
    CVPixelBufferLockBaseAddress(pix, kCVPixelBufferLock_ReadOnly);
    size_t w   = CVPixelBufferGetWidth(pix);
    size_t h   = CVPixelBufferGetHeight(pix);
    size_t bpr = CVPixelBufferGetBytesPerRow(pix);
    uint8_t* src = (uint8_t*)CVPixelBufferGetBaseAddress(pix);
    size_t needed = w * h * 4;

    if (!src) {
        sckit_copy_str("pixel buffer base address nil", err_msg, err_len);
    } else if ((int)needed > out_cap) {
        char tmp[128];
        snprintf(tmp, sizeof(tmp),
                 "buffer too small: need %zu got %d", needed, out_cap);
        sckit_copy_str(tmp, err_msg, err_len);
    } else {
        if (bpr == w * 4) {
            memcpy(out_pixels, src, needed);
        } else {
            for (size_t row = 0; row < h; row++) {
                memcpy(out_pixels + row * w * 4, src + row * bpr, w * 4);
            }
        }
        ret = (int)needed;
    }
    CVPixelBufferUnlockBaseAddress(pix, kCVPixelBufferLock_ReadOnly);
    CFRelease(buf);
    return ret;
}

int sckit_stream_stop(void* handle_raw) {
    if (!handle_raw) return -1;
    sckit_stream_t* handle = (sckit_stream_t*)handle_raw;
    SCStream* stream = (__bridge SCStream*)handle->stream;
    dispatch_semaphore_t sem = dispatch_semaphore_create(0);
    [stream stopCaptureWithCompletionHandler:^(NSError* err) {
        (void)err;
        dispatch_semaphore_signal(sem);
    }];
    dispatch_semaphore_wait(sem, DISPATCH_TIME_FOREVER);
    CFBridgingRelease(handle->stream);
    CFBridgingRelease(handle->sink);
    free(handle);
    return 0;
}

// ─── 4. Windows ───────────────────────────────────────────────
// Window enumeration uses the same SCShareableContent call. Strings are
// variable-length so we return them in a separate byte pool with offsets
// encoded in the per-window fixed-size struct.

typedef struct {
    uint32_t window_id;
    int32_t  frame_x;
    int32_t  frame_y;
    int32_t  frame_w;
    int32_t  frame_h;
    int32_t  pid;
    int32_t  layer;
    int32_t  on_screen;        // 0 or 1 — kept as int32 for Go struct alignment
    uint32_t app_name_offset;  // byte offset into the caller's string pool
    uint32_t app_name_len;
    uint32_t bundle_id_offset;
    uint32_t bundle_id_len;
    uint32_t title_offset;
    uint32_t title_len;
} sckit_window_t;  // 56 bytes, naturally aligned

// Helper: append a UTF-8 string to the string pool, return its offset/len.
// Returns 0 on failure (capacity exceeded); sets *out_offset=0 *out_len=0.
static BOOL append_string(const char* s, char* pool, int pool_cap,
                          int* pool_used,
                          uint32_t* out_offset, uint32_t* out_len) {
    if (!s) { *out_offset = 0; *out_len = 0; return YES; }
    size_t slen = strlen(s);
    if (*pool_used + (int)slen > pool_cap) {
        *out_offset = 0; *out_len = 0;
        return NO;
    }
    memcpy(pool + *pool_used, s, slen);
    *out_offset = (uint32_t)*pool_used;
    *out_len    = (uint32_t)slen;
    *pool_used += (int)slen;
    return YES;
}

// Returns count of windows written, -1 on fetch error, -2 on buffer too small.
// out_strings_used is set to the number of bytes used in the string pool.
int sckit_list_windows(sckit_window_t* out_windows, int max_windows,
                       char* out_strings, int max_string_bytes,
                       int32_t* out_strings_used,
                       char* err_msg, int err_len) {
    __block int count = -1;
    __block NSError* cap_err = nil;
    __block BOOL stringOverflow = NO;
    __block int pool_used = 0;
    dispatch_semaphore_t sem = dispatch_semaphore_create(0);

    [SCShareableContent getShareableContentWithCompletionHandler:
        ^(SCShareableContent* content, NSError* error) {
            if (error) { cap_err = error; dispatch_semaphore_signal(sem); return; }

            NSArray<SCWindow*>* windows = content.windows;
            int n = (int)windows.count;
            if (n > max_windows) n = max_windows;

            for (int i = 0; i < n; i++) {
                SCWindow* w = windows[i];
                SCRunningApplication* app = w.owningApplication;

                out_windows[i].window_id = (uint32_t)w.windowID;
                out_windows[i].frame_x   = (int32_t)w.frame.origin.x;
                out_windows[i].frame_y   = (int32_t)w.frame.origin.y;
                out_windows[i].frame_w   = (int32_t)w.frame.size.width;
                out_windows[i].frame_h   = (int32_t)w.frame.size.height;
                out_windows[i].pid       = app ? (int32_t)app.processID : 0;
                out_windows[i].layer     = (int32_t)w.windowLayer;
                out_windows[i].on_screen = w.onScreen ? 1 : 0;

                const char* appName  = app ? (app.applicationName.UTF8String   ?: "") : "";
                const char* bundleID = app ? (app.bundleIdentifier.UTF8String   ?: "") : "";
                const char* title    = w.title ? (w.title.UTF8String ?: "")           : "";

                if (!append_string(appName, out_strings, max_string_bytes,
                                   &pool_used,
                                   &out_windows[i].app_name_offset,
                                   &out_windows[i].app_name_len)) {
                    stringOverflow = YES; break;
                }
                if (!append_string(bundleID, out_strings, max_string_bytes,
                                   &pool_used,
                                   &out_windows[i].bundle_id_offset,
                                   &out_windows[i].bundle_id_len)) {
                    stringOverflow = YES; break;
                }
                if (!append_string(title, out_strings, max_string_bytes,
                                   &pool_used,
                                   &out_windows[i].title_offset,
                                   &out_windows[i].title_len)) {
                    stringOverflow = YES; break;
                }
            }
            count = n;
            dispatch_semaphore_signal(sem);
        }];

    dispatch_semaphore_wait(sem, DISPATCH_TIME_FOREVER);
    if (out_strings_used) *out_strings_used = (int32_t)pool_used;
    if (cap_err) { sckit_copy_err(cap_err, err_msg, err_len); return -1; }
    if (stringOverflow) {
        sckit_copy_str("string pool overflow — pass a larger out_strings buffer",
                       err_msg, err_len);
        return -2;
    }
    return count;
}

// ─── 5. Window one-shot capture ───────────────────────────────

int sckit_capture_window(uint32_t window_id,
                         const sckit_config_t* cfg,
                         uint8_t* out_pixels, int out_cap,
                         int32_t* out_width, int32_t* out_height,
                         char* err_msg, int err_len) {
    sckit_ensure_cg_init();
    __block int result = -1;
    __block NSError* cap_err = nil;
    dispatch_semaphore_t sem = dispatch_semaphore_create(0);

    [SCShareableContent getShareableContentWithCompletionHandler:
        ^(SCShareableContent* content, NSError* error) {
            if (error) { cap_err = error; dispatch_semaphore_signal(sem); return; }

            SCWindow* target = nil;
            for (SCWindow* w in content.windows) {
                if (w.windowID == window_id) { target = w; break; }
            }
            if (!target) {
                cap_err = [NSError errorWithDomain:@"sckit" code:404
                           userInfo:@{NSLocalizedDescriptionKey: @"window not found"}];
                dispatch_semaphore_signal(sem); return;
            }

            SCContentFilter* filter = [[SCContentFilter alloc]
                initWithDesktopIndependentWindow:target];
            SCStreamConfiguration* config = [[SCStreamConfiguration alloc] init];
            sckit_apply_config(config, cfg,
                               (int32_t)target.frame.size.width,
                               (int32_t)target.frame.size.height,
                               NULL, NULL);

            // captureSampleBufferWithFilter delivers a CMSampleBuffer whose
            // backing is an IOSurface-backed CVPixelBuffer. Avoids
            // CGBitmapContextCreate's WindowServer-connection requirement,
            // which trips CGS_REQUIRE_INIT when called from a CLI binary.
            [SCScreenshotManager captureSampleBufferWithFilter:filter
                                                 configuration:config
                                             completionHandler:^(CMSampleBufferRef buf, NSError* cerr) {
                    if (cerr || !buf) {
                        cap_err = cerr ?: [NSError errorWithDomain:@"sckit" code:1
                                          userInfo:@{NSLocalizedDescriptionKey: @"window capture returned nil"}];
                        dispatch_semaphore_signal(sem); return;
                    }
                    CVPixelBufferRef pix = CMSampleBufferGetImageBuffer(buf);
                    if (!pix) {
                        cap_err = [NSError errorWithDomain:@"sckit" code:2
                                   userInfo:@{NSLocalizedDescriptionKey: @"no pixel buffer in sample"}];
                        dispatch_semaphore_signal(sem); return;
                    }
                    CVPixelBufferLockBaseAddress(pix, kCVPixelBufferLock_ReadOnly);
                    size_t w   = CVPixelBufferGetWidth(pix);
                    size_t h   = CVPixelBufferGetHeight(pix);
                    size_t bpr = CVPixelBufferGetBytesPerRow(pix);
                    uint8_t* src = (uint8_t*)CVPixelBufferGetBaseAddress(pix);
                    size_t needed = w * h * 4;
                    if (out_width)  *out_width  = (int32_t)w;
                    if (out_height) *out_height = (int32_t)h;
                    if (!src) {
                        cap_err = [NSError errorWithDomain:@"sckit" code:4
                                   userInfo:@{NSLocalizedDescriptionKey: @"base address nil"}];
                    } else if ((int)needed > out_cap) {
                        NSString* d = [NSString stringWithFormat:
                            @"buffer too small: need %zu got %d", needed, out_cap];
                        cap_err = [NSError errorWithDomain:@"sckit" code:2
                                   userInfo:@{NSLocalizedDescriptionKey: d}];
                    } else {
                        if (bpr == w * 4) {
                            memcpy(out_pixels, src, needed);
                        } else {
                            for (size_t row = 0; row < h; row++) {
                                memcpy(out_pixels + row * w * 4, src + row * bpr, w * 4);
                            }
                        }
                        result = (int)needed;
                    }
                    CVPixelBufferUnlockBaseAddress(pix, kCVPixelBufferLock_ReadOnly);
                    dispatch_semaphore_signal(sem);
                }];
        }];

    dispatch_semaphore_wait(sem, DISPATCH_TIME_FOREVER);
    if (cap_err) sckit_copy_err(cap_err, err_msg, err_len);
    return result;
}

// ─── 6. Window persistent stream ──────────────────────────────
// Shares sckit_stream_next_frame / sckit_stream_stop with the display
// stream (same sckit_stream_t handle shape). Only the filter construction
// differs.

void* sckit_window_stream_start(uint32_t window_id,
                                const sckit_config_t* cfg,
                                char* err_msg, int err_len) {
    sckit_ensure_cg_init();
    __block SCStream* stream = nil;
    __block NSError* cap_err = nil;
    __block int32_t eff_w = 0, eff_h = 0;
    SCKitFrameSink* sink = [[SCKitFrameSink alloc] init];
    dispatch_semaphore_t startSem = dispatch_semaphore_create(0);

    [SCShareableContent getShareableContentWithCompletionHandler:
        ^(SCShareableContent* content, NSError* error) {
            if (error) { cap_err = error; dispatch_semaphore_signal(startSem); return; }
            SCWindow* target = nil;
            for (SCWindow* w in content.windows) {
                if (w.windowID == window_id) { target = w; break; }
            }
            if (!target) {
                cap_err = [NSError errorWithDomain:@"sckit" code:404
                           userInfo:@{NSLocalizedDescriptionKey: @"window not found"}];
                dispatch_semaphore_signal(startSem); return;
            }
            SCContentFilter* filter = [[SCContentFilter alloc]
                initWithDesktopIndependentWindow:target];
            SCStreamConfiguration* config = [[SCStreamConfiguration alloc] init];
            sckit_apply_config(config, cfg,
                               (int32_t)target.frame.size.width,
                               (int32_t)target.frame.size.height,
                               &eff_w, &eff_h);

            SCStream* s = [[SCStream alloc] initWithFilter:filter
                                            configuration:config
                                                 delegate:sink];
            NSError* addErr = nil;
            BOOL ok = [s addStreamOutput:sink
                                    type:SCStreamOutputTypeScreen
                      sampleHandlerQueue:dispatch_get_global_queue(QOS_CLASS_USER_INTERACTIVE, 0)
                                   error:&addErr];
            if (!ok) {
                cap_err = addErr;
                dispatch_semaphore_signal(startSem);
                return;
            }
            [s startCaptureWithCompletionHandler:^(NSError* startErr) {
                if (startErr) cap_err = startErr;
                else          stream  = s;
                dispatch_semaphore_signal(startSem);
            }];
        }];

    dispatch_semaphore_wait(startSem, DISPATCH_TIME_FOREVER);
    if (cap_err || !stream) {
        if (cap_err) sckit_copy_err(cap_err, err_msg, err_len);
        return NULL;
    }

    sckit_stream_t* handle = calloc(1, sizeof(sckit_stream_t));
    handle->stream = (void*)CFBridgingRetain(stream);
    handle->sink   = (void*)CFBridgingRetain(sink);
    handle->width  = eff_w;
    handle->height = eff_h;
    return handle;
}

// ─── 7. App target ────────────────────────────────────────────
// Capture all on-screen windows of an application composed together on
// a given display. If display_id == 0, auto-picks the display that owns
// the largest share of the app's windows (falls back to the first
// display).

// Find the SCDisplay whose frame contains the bulk of `app_windows`.
// Returns nil if no app window is on-screen.
static SCDisplay* sckit_display_for_app(SCShareableContent* content,
                                        NSArray<SCWindow*>* app_windows) {
    if (app_windows.count == 0) return nil;
    // Count on-screen area per display.
    NSMutableDictionary<NSNumber*, NSNumber*>* area_by_display = [NSMutableDictionary dictionary];
    for (SCWindow* w in app_windows) {
        if (!w.onScreen) continue;
        for (SCDisplay* d in content.displays) {
            CGRect inter = CGRectIntersection(d.frame, w.frame);
            if (!CGRectIsNull(inter) && !CGRectIsEmpty(inter)) {
                double area = inter.size.width * inter.size.height;
                NSNumber* key = @(d.displayID);
                area_by_display[key] = @(area_by_display[key].doubleValue + area);
            }
        }
    }
    __block SCDisplay* best = nil;
    __block double best_area = 0;
    for (SCDisplay* d in content.displays) {
        double a = area_by_display[@(d.displayID)].doubleValue;
        if (a > best_area) { best = d; best_area = a; }
    }
    return best ?: content.displays.firstObject;
}

int sckit_capture_app(const char* bundle_id,
                      uint32_t display_id,            // 0 = auto
                      const sckit_config_t* cfg,
                      uint8_t* out_pixels, int out_cap,
                      int32_t* out_width, int32_t* out_height,
                      char* err_msg, int err_len) {
    sckit_ensure_cg_init();
    if (!bundle_id || !*bundle_id) {
        sckit_copy_str("bundle_id required", err_msg, err_len);
        return -1;
    }
    NSString* bid = [NSString stringWithUTF8String:bundle_id];
    __block int result = -1;
    __block NSError* cap_err = nil;
    dispatch_semaphore_t sem = dispatch_semaphore_create(0);

    [SCShareableContent getShareableContentWithCompletionHandler:
        ^(SCShareableContent* content, NSError* error) {
            if (error) { cap_err = error; dispatch_semaphore_signal(sem); return; }

            // Collect windows + the running app for this bundle.
            NSMutableArray<SCWindow*>* app_windows = [NSMutableArray array];
            SCRunningApplication* app = nil;
            for (SCWindow* w in content.windows) {
                if ([w.owningApplication.bundleIdentifier isEqualToString:bid]) {
                    [app_windows addObject:w];
                    if (!app) app = w.owningApplication;
                }
            }
            if (!app) {
                cap_err = [NSError errorWithDomain:@"sckit" code:404
                           userInfo:@{NSLocalizedDescriptionKey:
                             [NSString stringWithFormat:@"no running app with bundle %@", bid]}];
                dispatch_semaphore_signal(sem); return;
            }

            // Pick display.
            SCDisplay* target = nil;
            if (display_id != 0) {
                for (SCDisplay* d in content.displays) {
                    if (d.displayID == display_id) { target = d; break; }
                }
            } else {
                target = sckit_display_for_app(content, app_windows);
            }
            if (!target) {
                cap_err = [NSError errorWithDomain:@"sckit" code:404
                           userInfo:@{NSLocalizedDescriptionKey: @"no display for app"}];
                dispatch_semaphore_signal(sem); return;
            }

            NSArray<SCWindow*>* excl = sckit_resolve_windows(content,
                cfg ? cfg->exclude_ids : NULL,
                cfg ? cfg->n_exclude   : 0);
            SCContentFilter* filter = [[SCContentFilter alloc]
                initWithDisplay:target
                includingApplications:@[app]
                exceptingWindows:excl];
            SCStreamConfiguration* config = [[SCStreamConfiguration alloc] init];
            sckit_apply_config(config, cfg,
                               (int32_t)target.width, (int32_t)target.height,
                               NULL, NULL);

            [SCScreenshotManager captureSampleBufferWithFilter:filter
                                                 configuration:config
                                             completionHandler:^(CMSampleBufferRef buf, NSError* cerr) {
                    if (cerr || !buf) {
                        cap_err = cerr ?: [NSError errorWithDomain:@"sckit" code:1
                                          userInfo:@{NSLocalizedDescriptionKey: @"app capture returned nil"}];
                        dispatch_semaphore_signal(sem); return;
                    }
                    CVPixelBufferRef pix = CMSampleBufferGetImageBuffer(buf);
                    if (!pix) {
                        cap_err = [NSError errorWithDomain:@"sckit" code:2
                                   userInfo:@{NSLocalizedDescriptionKey: @"no pixel buffer"}];
                        dispatch_semaphore_signal(sem); return;
                    }
                    CVPixelBufferLockBaseAddress(pix, kCVPixelBufferLock_ReadOnly);
                    size_t w   = CVPixelBufferGetWidth(pix);
                    size_t h   = CVPixelBufferGetHeight(pix);
                    size_t bpr = CVPixelBufferGetBytesPerRow(pix);
                    uint8_t* src = (uint8_t*)CVPixelBufferGetBaseAddress(pix);
                    size_t needed = w * h * 4;
                    if (out_width)  *out_width  = (int32_t)w;
                    if (out_height) *out_height = (int32_t)h;
                    if (!src) {
                        cap_err = [NSError errorWithDomain:@"sckit" code:4
                                   userInfo:@{NSLocalizedDescriptionKey: @"base address nil"}];
                    } else if ((int)needed > out_cap) {
                        NSString* d = [NSString stringWithFormat:
                            @"buffer too small: need %zu got %d", needed, out_cap];
                        cap_err = [NSError errorWithDomain:@"sckit" code:2
                                   userInfo:@{NSLocalizedDescriptionKey: d}];
                    } else {
                        if (bpr == w * 4) {
                            memcpy(out_pixels, src, needed);
                        } else {
                            for (size_t row = 0; row < h; row++) {
                                memcpy(out_pixels + row * w * 4, src + row * bpr, w * 4);
                            }
                        }
                        result = (int)needed;
                    }
                    CVPixelBufferUnlockBaseAddress(pix, kCVPixelBufferLock_ReadOnly);
                    dispatch_semaphore_signal(sem);
                }];
        }];

    dispatch_semaphore_wait(sem, DISPATCH_TIME_FOREVER);
    if (cap_err) sckit_copy_err(cap_err, err_msg, err_len);
    return result;
}

void* sckit_app_stream_start(const char* bundle_id,
                             uint32_t display_id,
                             const sckit_config_t* cfg,
                             char* err_msg, int err_len) {
    sckit_ensure_cg_init();
    if (!bundle_id || !*bundle_id) {
        sckit_copy_str("bundle_id required", err_msg, err_len);
        return NULL;
    }
    NSString* bid = [NSString stringWithUTF8String:bundle_id];
    __block SCStream* stream = nil;
    __block NSError* cap_err = nil;
    __block int32_t eff_w = 0, eff_h = 0;
    SCKitFrameSink* sink = [[SCKitFrameSink alloc] init];
    dispatch_semaphore_t startSem = dispatch_semaphore_create(0);

    [SCShareableContent getShareableContentWithCompletionHandler:
        ^(SCShareableContent* content, NSError* error) {
            if (error) { cap_err = error; dispatch_semaphore_signal(startSem); return; }

            NSMutableArray<SCWindow*>* app_windows = [NSMutableArray array];
            SCRunningApplication* app = nil;
            for (SCWindow* w in content.windows) {
                if ([w.owningApplication.bundleIdentifier isEqualToString:bid]) {
                    [app_windows addObject:w];
                    if (!app) app = w.owningApplication;
                }
            }
            if (!app) {
                cap_err = [NSError errorWithDomain:@"sckit" code:404
                           userInfo:@{NSLocalizedDescriptionKey:
                             [NSString stringWithFormat:@"no running app with bundle %@", bid]}];
                dispatch_semaphore_signal(startSem); return;
            }

            SCDisplay* target = nil;
            if (display_id != 0) {
                for (SCDisplay* d in content.displays) {
                    if (d.displayID == display_id) { target = d; break; }
                }
            } else {
                target = sckit_display_for_app(content, app_windows);
            }
            if (!target) {
                cap_err = [NSError errorWithDomain:@"sckit" code:404
                           userInfo:@{NSLocalizedDescriptionKey: @"no display for app"}];
                dispatch_semaphore_signal(startSem); return;
            }

            NSArray<SCWindow*>* excl = sckit_resolve_windows(content,
                cfg ? cfg->exclude_ids : NULL,
                cfg ? cfg->n_exclude   : 0);
            SCContentFilter* filter = [[SCContentFilter alloc]
                initWithDisplay:target
                includingApplications:@[app]
                exceptingWindows:excl];
            SCStreamConfiguration* config = [[SCStreamConfiguration alloc] init];
            sckit_apply_config(config, cfg,
                               (int32_t)target.width, (int32_t)target.height,
                               &eff_w, &eff_h);

            SCStream* s = [[SCStream alloc] initWithFilter:filter
                                            configuration:config
                                                 delegate:sink];
            NSError* addErr = nil;
            BOOL ok = [s addStreamOutput:sink
                                    type:SCStreamOutputTypeScreen
                      sampleHandlerQueue:dispatch_get_global_queue(QOS_CLASS_USER_INTERACTIVE, 0)
                                   error:&addErr];
            if (!ok) {
                cap_err = addErr;
                dispatch_semaphore_signal(startSem);
                return;
            }
            [s startCaptureWithCompletionHandler:^(NSError* startErr) {
                if (startErr) cap_err = startErr;
                else          stream  = s;
                dispatch_semaphore_signal(startSem);
            }];
        }];

    dispatch_semaphore_wait(startSem, DISPATCH_TIME_FOREVER);
    if (cap_err || !stream) {
        if (cap_err) sckit_copy_err(cap_err, err_msg, err_len);
        return NULL;
    }

    sckit_stream_t* handle = calloc(1, sizeof(sckit_stream_t));
    handle->stream = (void*)CFBridgingRetain(stream);
    handle->sink   = (void*)CFBridgingRetain(sink);
    handle->width  = eff_w;
    handle->height = eff_h;
    return handle;
}


// ─── OCR via Vision framework ─────────────────────────────────
//
// VNRecognizeTextRequest reads a CGImage and returns text observations
// with bounding boxes (normalized 0-1, bottom-left origin in Vision)
// and confidence scores. The wrapper accepts arbitrary image bytes
// (PNG / JPEG / TIFF — anything NSImage can decode), runs the request
// synchronously, returns JSON to Go.
//
// Output JSON:
//   [{"text": "...", "x": <px>, "y": <px>, "w": <px>, "h": <px>, "conf": 0.95}, ...]
//
// Coordinates are converted to image-pixel space with top-left origin
// (the convention KinClaw + Go consumers expect — matches CGImage's
// drawing convention, not Vision's).
//
// Recognition level: VNRequestTextRecognitionLevelAccurate (default).
// Language correction: ON (improves results on noisy screen captures).

#import <Vision/Vision.h>

int sckit_ocr_image(const void* img_bytes, int img_len,
                    char* out_json, int out_cap,
                    char* err_msg, int err_len) {
    if (!img_bytes || img_len <= 0) {
        sckit_copy_str("empty image bytes", err_msg, err_len);
        return -1;
    }
    if (!out_json || out_cap <= 0) {
        sckit_copy_str("empty output buffer", err_msg, err_len);
        return -1;
    }
    @autoreleasepool {
        NSData* data = [NSData dataWithBytes:img_bytes length:(NSUInteger)img_len];
        NSImage* nsImg = [[NSImage alloc] initWithData:data];
        if (!nsImg) {
            sckit_copy_str("NSImage decode failed", err_msg, err_len);
            return -1;
        }
        // Convert NSImage → CGImage. Pin to actual pixel size so OCR
        // operates on the source resolution (not the @1x rendering).
        NSRect rect = NSMakeRect(0, 0, nsImg.size.width, nsImg.size.height);
        CGImageRef cgImg = [nsImg CGImageForProposedRect:&rect context:nil hints:nil];
        if (!cgImg) {
            sckit_copy_str("CGImage extraction failed", err_msg, err_len);
            return -1;
        }
        size_t imgW = CGImageGetWidth(cgImg);
        size_t imgH = CGImageGetHeight(cgImg);

        VNImageRequestHandler* handler =
            [[VNImageRequestHandler alloc] initWithCGImage:cgImg options:@{}];
        VNRecognizeTextRequest* req = [[VNRecognizeTextRequest alloc] init];
        req.recognitionLevel = VNRequestTextRecognitionLevelAccurate;
        req.usesLanguageCorrection = YES;

        NSError* runErr = nil;
        BOOL ok = [handler performRequests:@[req] error:&runErr];
        if (!ok || runErr) {
            sckit_copy_err(runErr ?: [NSError errorWithDomain:@"sckit"
                                                         code:-1
                                                     userInfo:@{NSLocalizedDescriptionKey:@"VNRecognizeTextRequest failed"}],
                           err_msg, err_len);
            return -1;
        }

        NSMutableArray* regions = [NSMutableArray arrayWithCapacity:req.results.count];
        for (VNRecognizedTextObservation* obs in req.results) {
            VNRecognizedText* top = [obs topCandidates:1].firstObject;
            if (!top) continue;
            CGRect bbox = obs.boundingBox;
            // Vision: normalized 0-1, bottom-left. Convert to pixel,
            // top-left.
            double pxX = bbox.origin.x * (double)imgW;
            double pxY = (double)imgH - (bbox.origin.y + bbox.size.height) * (double)imgH;
            double pxW = bbox.size.width  * (double)imgW;
            double pxH = bbox.size.height * (double)imgH;
            [regions addObject:@{
                @"text": top.string ?: @"",
                @"x":    @((int)round(pxX)),
                @"y":    @((int)round(pxY)),
                @"w":    @((int)round(pxW)),
                @"h":    @((int)round(pxH)),
                @"conf": @(top.confidence),
            }];
        }

        NSError* jsonErr = nil;
        NSData* jsonData = [NSJSONSerialization dataWithJSONObject:regions
                                                           options:0
                                                             error:&jsonErr];
        if (!jsonData || jsonErr) {
            sckit_copy_err(jsonErr ?: [NSError errorWithDomain:@"sckit"
                                                          code:-2
                                                      userInfo:@{NSLocalizedDescriptionKey:@"JSON encoding failed"}],
                           err_msg, err_len);
            return -1;
        }
        const char* utf8 = [[[NSString alloc] initWithData:jsonData
                                                  encoding:NSUTF8StringEncoding] UTF8String];
        if (!utf8) {
            sckit_copy_str("UTF-8 conversion failed", err_msg, err_len);
            return -1;
        }
        int needed = (int)strlen(utf8) + 1;
        if (needed > out_cap) {
            // Caller should resize and retry. Same overflow convention
            // as kinax-go's string copies: positive return = required cap.
            return needed;
        }
        memcpy(out_json, utf8, (size_t)needed);
        return 0;
    }
}
