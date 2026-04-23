# Security Policy

## Supported versions

| Version | Supported |
|---------|-----------|
| v0.x    | ✅ Latest minor (active development)
| < v0.x  | ❌

Once v1.0.0 ships, this table will track a rolling window of the two
most-recent minor versions.

## Reporting a vulnerability

Please **do not** file a public issue for security reports.

Email `security@localkin.ai` with:
- A description of the vulnerability.
- Steps to reproduce.
- A minimal PoC if available.
- Your expected timeline for public disclosure.

You'll get a response within 48 hours. Embargo window: 30 days or until
a fix is released (whichever is sooner), unless you need longer for
coordinated disclosure.

## Threat model

sckit-go loads a dylib into the host process and calls ScreenCaptureKit
APIs. Concrete threat surface:

1. **Embedded dylib integrity**: the dylib is committed to this repo
   under `internal/dylib/libsckit_sync.dylib`. Any change to that file
   is visible in `git diff`. Verify the hash against release notes
   before adopting a new version.
2. **Cache extraction**: on first use, the embedded dylib is extracted
   to `~/Library/Caches/sckit-go/<sha256-prefix>/`. The directory name
   is the content hash so cache tampering surfaces as a mismatch.
3. **TCC permission**: sckit-go cannot bypass macOS Screen Recording
   permission. If a process obtains screen access through sckit-go,
   the user explicitly granted it.
4. **Pixel data**: captured frames pass through Go-owned memory. They
   never touch disk unless the caller explicitly writes them.

## Out of scope

- macOS itself (report to Apple).
- `purego` or other dependencies (report upstream).
- Consumer-of-sckit-go apps that do bad things with captured frames
  (that's an application concern, not a library one).
