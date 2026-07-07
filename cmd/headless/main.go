// Command headless runs the sim from a script file: spawn a scenario, feed
// scheduled commands, watch what happens. The debug client until there's a
// real one.
//
//	go run ./cmd/headless -script scripts/slice.json -ticks 300
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/JakeMalmrose/draupforge/content"
	"github.com/JakeMalmrose/draupforge/protocol"
	"github.com/JakeMalmrose/draupforge/server"
	"github.com/JakeMalmrose/draupforge/sim"
	"github.com/JakeMalmrose/draupforge/sim/core"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

func main() {
	scriptPath := flag.String("script", "", "path to scenario script (JSON)")
	replayPath := flag.String("replay", "", "replay a server-recorded segment (server -replaydir) instead of a script")
	ticks := flag.Uint64("ticks", 300, "ticks to simulate")
	seed := flag.Uint64("seed", 1, "world seed")
	snapEvery := flag.Uint64("snap-every", 0, "print a snapshot every N ticks (0 = final only)")
	printHash := flag.Bool("hash", false, "print the world state hash every tick")
	quiet := flag.Bool("quiet", false, "suppress event lines")
	flag.Parse()

	if *replayPath != "" {
		runReplay(*replayPath, *printHash, *quiet)
		return
	}
	if *scriptPath == "" {
		fmt.Fprintln(os.Stderr, "headless: -script or -replay is required")
		os.Exit(2)
	}
	raw, err := os.ReadFile(*scriptPath)
	if err != nil {
		fatal(err)
	}
	var script protocol.Script
	if err := json.Unmarshal(raw, &script); err != nil {
		fatal(fmt.Errorf("parsing script: %w", err))
	}

	s := sim.New(content.DB(), *seed)
	if script.Map != nil {
		s.GenerateMap(space.MapSpec{
			Width: script.Map.Width, Height: script.Map.Height, Rooms: script.Map.Rooms,
		})
		for _, row := range s.EncodeMap().Rows {
			fmt.Println(row)
		}
	}
	for i, sp := range script.Spawns {
		id, err := s.Spawn(sp.Def, space.V(fm.FromMilli(sp.X), fm.FromMilli(sp.Y)))
		if err != nil {
			fatal(err)
		}
		for _, gem := range sp.Gems {
			if err := s.GrantGem(id, gem, 1); err != nil {
				fatal(err)
			}
		}
		fmt.Printf("spawned %s as entity %d (spawn #%d)\n", sp.Def, id, i+1)
	}
	for _, sc := range script.Scatter {
		if err := s.ScatterSpawn(sc.Def, sc.Count); err != nil {
			fatal(err)
		}
		fmt.Printf("scattered %d × %s\n", sc.Count, sc.Def)
	}

	byTick := map[uint64][]core.Command{}
	for _, wc := range script.Commands {
		c, err := sim.DecodeCommand(wc)
		if err != nil {
			fatal(err)
		}
		byTick[wc.Tick] = append(byTick[wc.Tick], c)
	}

	for t := uint64(1); t <= *ticks; t++ {
		s.Step(byTick[t])
		if *printHash {
			fmt.Printf("tick %4d hash %016x\n", t, s.W.Hash())
		}
		if !*quiet {
			for _, ev := range s.W.LastEvents {
				printEvent(ev)
			}
		}
		if *snapEvery > 0 && t%*snapEvery == 0 {
			printSnapshot(s)
		}
	}

	fmt.Println("--- final state ---")
	printSnapshot(s)
}

// runReplay re-executes a server-recorded segment: the recorded world plus
// its command lines, tick for tick. The final hash is the repro currency —
// run it twice, or against a patched build, and diff.
func runReplay(path string, printHash, quiet bool) {
	w, err := server.Replay(content.DB(), path,
		func(w *core.World, cmds []core.Command) {
			(&sim.Sim{W: w}).Step(cmds)
		},
		func(w *core.World) {
			if printHash {
				fmt.Printf("tick %4d hash %016x\n", w.Tick, w.Hash())
			}
			if !quiet {
				for _, ev := range w.LastEvents {
					printEvent(ev)
				}
			}
		})
	if err != nil {
		fatal(err)
	}
	fmt.Printf("replay complete: tick %d hash %016x\n", w.Tick, w.Hash())
}

func printEvent(ev core.Event) {
	switch ev.Kind {
	case core.EvHit:
		crit := ""
		if ev.Crit {
			crit = " CRIT"
		}
		fmt.Printf("[tick %4d] %d hit %d for %s (%s)%s\n", ev.Tick, ev.Actor, ev.Other, fixedStr(ev.Amount), ev.Note, crit)
	case core.EvMiss:
		fmt.Printf("[tick %4d] %d missed %d (%s)\n", ev.Tick, ev.Actor, ev.Other, ev.Note)
	case core.EvDeath:
		fmt.Printf("[tick %4d] %d died (killer: %d)\n", ev.Tick, ev.Actor, ev.Other)
	case core.EvIgnite:
		fmt.Printf("[tick %4d] %d ignited %d (%s/tick)\n", ev.Tick, ev.Actor, ev.Other, fixedStr(ev.Amount))
	case core.EvDrop:
		fmt.Printf("[tick %4d] %d dropped %s (entity %d)\n", ev.Tick, ev.Actor, ev.Note, ev.Other)
	case core.EvEquip:
		fmt.Printf("[tick %4d] %d equipped %s (item %d)\n", ev.Tick, ev.Actor, ev.Note, ev.Other)
	case core.EvPickup:
		fmt.Printf("[tick %4d] %d picked up %s (item %d)\n", ev.Tick, ev.Actor, ev.Note, ev.Other)
	case core.EvUnequip:
		fmt.Printf("[tick %4d] %d unequipped %s (item %d)\n", ev.Tick, ev.Actor, ev.Note, ev.Other)
	case core.EvChill:
		fmt.Printf("[tick %4d] %d chilled %d (%s slow)\n", ev.Tick, ev.Actor, ev.Other, fixedStr(ev.Amount))
	case core.EvShock:
		fmt.Printf("[tick %4d] %d shocked %d (+%s taken)\n", ev.Tick, ev.Actor, ev.Other, fixedStr(ev.Amount))
	case core.EvBleed:
		fmt.Printf("[tick %4d] %d tore a bleed on %d (%s/tick)\n", ev.Tick, ev.Actor, ev.Other, fixedStr(ev.Amount))
	case core.EvPoison:
		fmt.Printf("[tick %4d] %d poisoned %d (%s/tick, stacking)\n", ev.Tick, ev.Actor, ev.Other, fixedStr(ev.Amount))
	case core.EvAura:
		state := "off"
		if ev.Amount > 0 {
			state = "on"
		}
		fmt.Printf("[tick %4d] %d toggled %s %s\n", ev.Tick, ev.Actor, ev.Note, state)
	case core.EvCurse:
		fmt.Printf("[tick %4d] %d cursed %d with %s\n", ev.Tick, ev.Actor, ev.Other, ev.Note)
	}
}

func fixedStr(f fm.Fixed) string {
	return fmt.Sprintf("%d.%03d", f.Milli()/1000, abs(f.Milli()%1000))
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

func printSnapshot(s *sim.Sim) {
	out, err := json.MarshalIndent(s.BuildSnapshot(), "", "  ")
	if err != nil {
		fatal(err)
	}
	fmt.Println(string(out))
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "headless:", err)
	os.Exit(1)
}
