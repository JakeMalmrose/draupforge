package space

import (
	"fmt"

	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
)

// GridSave is a Grid's flat serialized form. Rows encode both layers the
// grid actually carries — '#' solid, '.' walkable floor, ',' floor a
// Clearance circle can't stand on (erosion or unreachable-pruning). Walk
// flags are saved rather than re-derived because pruning is history (it ran
// against the generator's spawn point); a restored grid must not re-decide.
type GridSave struct {
	Width     int      `json:"width"`
	Height    int      `json:"height"`
	Tile      fm.Fixed `json:"tile"`
	Clearance fm.Fixed `json:"clearance"`
	Spawn     Vec2     `json:"spawn"`
	Exit      Vec2     `json:"exit"`
	Rows      []string `json:"rows"`
}

// Encode returns the grid's serialized form.
func (g *Grid) Encode() GridSave {
	rows := make([]string, g.Height)
	buf := make([]byte, g.Width)
	for y := 0; y < g.Height; y++ {
		for x := 0; x < g.Width; x++ {
			switch {
			case g.Solid(x, y):
				buf[x] = '#'
			case g.walkAt(x, y):
				buf[x] = '.'
			default:
				buf[x] = ','
			}
		}
		rows[y] = string(buf)
	}
	return GridSave{
		Width: g.Width, Height: g.Height,
		Tile: g.Tile, Clearance: g.Clearance,
		Spawn: g.Spawn, Exit: g.Exit, Rows: rows,
	}
}

// DecodeGrid rebuilds a grid bit-exactly from its saved form: solid and walk
// layers come from the rows; hash words, walkable centers, and pathing
// scratch are re-derived.
func DecodeGrid(s GridSave) (*Grid, error) {
	if s.Width <= 0 || s.Height <= 0 || len(s.Rows) != s.Height {
		return nil, fmt.Errorf("space: grid save is %dx%d with %d rows", s.Width, s.Height, len(s.Rows))
	}
	g := NewGrid(s.Width, s.Height, s.Tile, s.Clearance)
	g.Spawn = s.Spawn
	g.Exit = s.Exit
	for y, row := range s.Rows {
		if len(row) != s.Width {
			return nil, fmt.Errorf("space: grid save row %d is %d wide, want %d", y, len(row), s.Width)
		}
		for x := 0; x < s.Width; x++ {
			if row[x] != '#' {
				g.SetSolid(x, y, false)
			}
		}
	}
	g.Finalize()
	// Overwrite the derived walk layer with the saved one — Finalize knows
	// erosion but not the pruning that already happened.
	for i := range g.walk {
		g.walk[i] = s.Rows[i/s.Width][i%s.Width] == '.'
	}
	g.walkC = g.walkC[:0]
	for i, ok := range g.walk {
		if ok {
			g.walkC = append(g.walkC, g.TileCenter(i%s.Width, i/s.Width))
		}
	}
	return g, nil
}
