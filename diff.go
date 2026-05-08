package sckit

import (
	"fmt"
	"image"
	"strings"
)

// DiffGrid is the result of [DiffImages]. Cells is a row-major
// [rows][cols] matrix of mean-abs-delta values per grid cell (0..255
// scale). Use [DiffGrid.Dirty] / [DiffGrid.BoundingBox] /
// [DiffGrid.Render] for the common downstream operations.
type DiffGrid struct {
	// Cells holds the per-cell mean-abs-delta. Cells[r][c] is the
	// average per-pixel intensity delta in that cell. Rows × Cols
	// equals the grid resolution requested in [DiffImages].
	Cells [][]float64
	// Rows / Cols echo the requested resolution (so callers don't
	// have to len() the slice every time).
	Rows, Cols int
	// Bounds is the image rectangle the diff was computed over. Used
	// by [BoundingBox] to map grid cells back to display-local px.
	Bounds image.Rectangle
}

// DiffImages compares two images of the same dimensions over a
// rows × cols grid and returns mean-abs-delta of grayscale intensity
// per cell. Used as a token-cheap alternative to "ask a vision LLM
// to compare two screenshots" for action verification — change in
// any cell above a threshold means the world's reacted; absence means
// the click was ignored / page didn't update / element didn't appear.
//
// Sampling: every 4th pixel in each axis (per-cell). Keeps diff fast
// on retina-resolution captures while still catching text-shaped
// changes (text edges average out at sub-cell scale).
//
// Errors:
//
//   - dimension mismatch: a and b must have identical bounds.
//
// Typical use:
//
//	before, _ := sckit.Capture(ctx, target)
//	// … action happens here, optional sleep …
//	after, _ := sckit.Capture(ctx, target)
//	grid, err := sckit.DiffImages(before, after, 16, 16)
//	if grid.Dirty(8) > 0 {
//	    bbox, _ := grid.BoundingBox(8)
//	    fmt.Println("UI changed in", bbox)
//	}
//
// 16×16 is the common default — fine enough to localize one button's
// worth of change, coarse enough to ignore antialiasing noise.
func DiffImages(a, b image.Image, rows, cols int) (*DiffGrid, error) {
	if rows <= 0 || cols <= 0 {
		return nil, fmt.Errorf("sckit.DiffImages: rows and cols must be > 0")
	}
	ab := a.Bounds()
	bb := b.Bounds()
	if ab != bb {
		return nil, fmt.Errorf("sckit.DiffImages: dimension mismatch (%v vs %v)", ab, bb)
	}
	cw := ab.Dx() / cols
	ch := ab.Dy() / rows
	cells := make([][]float64, rows)
	for r := 0; r < rows; r++ {
		cells[r] = make([]float64, cols)
		for c := 0; c < cols; c++ {
			x0 := ab.Min.X + c*cw
			y0 := ab.Min.Y + r*ch
			x1 := x0 + cw
			y1 := y0 + ch
			if c == cols-1 {
				x1 = ab.Max.X
			}
			if r == rows-1 {
				y1 = ab.Max.Y
			}
			cells[r][c] = meanAbsDelta(a, b, x0, y0, x1, y1)
		}
	}
	return &DiffGrid{Cells: cells, Rows: rows, Cols: cols, Bounds: ab}, nil
}

// Dirty returns the number of cells whose mean-abs-delta is at or
// above threshold (0..255). Use this as a cheap "did anything
// actually change?" check before calling [BoundingBox] / [Render].
func (g *DiffGrid) Dirty(threshold float64) int {
	n := 0
	for _, row := range g.Cells {
		for _, v := range row {
			if v >= threshold {
				n++
			}
		}
	}
	return n
}

// BoundingBox returns the union rectangle of all cells whose value
// crosses threshold, mapped back to display-local px coordinates
// (using g.Bounds + cell stride). Returns ok=false when nothing's
// dirty — caller should report "no change" rather than draw an
// empty rect.
func (g *DiffGrid) BoundingBox(threshold float64) (image.Rectangle, bool) {
	minR, minC := g.Rows, g.Cols
	maxR, maxC := -1, -1
	for r := 0; r < g.Rows; r++ {
		for c := 0; c < g.Cols; c++ {
			if g.Cells[r][c] < threshold {
				continue
			}
			if r < minR {
				minR = r
			}
			if c < minC {
				minC = c
			}
			if r > maxR {
				maxR = r
			}
			if c > maxC {
				maxC = c
			}
		}
	}
	if maxR < 0 {
		return image.Rectangle{}, false
	}
	cw := g.Bounds.Dx() / g.Cols
	ch := g.Bounds.Dy() / g.Rows
	x0 := g.Bounds.Min.X + minC*cw
	y0 := g.Bounds.Min.Y + minR*ch
	x1 := g.Bounds.Min.X + (maxC+1)*cw
	y1 := g.Bounds.Min.Y + (maxR+1)*ch
	if x1 > g.Bounds.Max.X {
		x1 = g.Bounds.Max.X
	}
	if y1 > g.Bounds.Max.Y {
		y1 = g.Bounds.Max.Y
	}
	return image.Rect(x0, y0, x1, y1), true
}

// Render produces a textual heatmap of the grid for human / LLM
// inspection. '#' = dirty (≥ threshold), '.' = warm (≥ threshold/2),
// ' ' = quiet. One row per grid row, no header / footer — caller
// adds those if needed. Used by kinclaw's screen.diff_screenshots
// verb to send a token-cheap visual to the model.
func (g *DiffGrid) Render(threshold float64) string {
	var sb strings.Builder
	for r := 0; r < g.Rows; r++ {
		for c := 0; c < g.Cols; c++ {
			v := g.Cells[r][c]
			switch {
			case v >= threshold:
				sb.WriteByte('#')
			case v >= threshold/2:
				sb.WriteByte('.')
			default:
				sb.WriteByte(' ')
			}
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

// meanAbsDelta samples a coarse subset of pixels in the rect (every
// 4th pixel in each axis) and returns mean abs delta of grayscale
// intensity (0..255). Internal helper; not exported because the
// interesting unit-of-work is the whole grid via [DiffImages].
func meanAbsDelta(a, b image.Image, x0, y0, x1, y1 int) float64 {
	var sum, n int
	const stride = 4
	for y := y0; y < y1; y += stride {
		for x := x0; x < x1; x += stride {
			ar, ag, ab_, _ := a.At(x, y).RGBA()
			br, bg, bb_, _ := b.At(x, y).RGBA()
			ai := int((ar + ag + ab_) / 3 / 257) // 16-bit → 8-bit via /257
			bi := int((br + bg + bb_) / 3 / 257)
			d := ai - bi
			if d < 0 {
				d = -d
			}
			sum += d
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return float64(sum) / float64(n)
}
