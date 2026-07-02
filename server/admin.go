// Admin dashboard + JSON API on its own port (Config.AdminAddr) — the
// observe/poke tier of the dashboard parked in RISKS.md. Save/load/rollback
// wait on the persistence story (RISKS.md #1).
//
// Every handler that touches the world enqueues a closure that the tick
// goroutine runs between ticks (adminOp in server.go), so the sim's
// single-goroutine invariant holds and every response is a consistent
// between-ticks view. No auth: bind this to localhost or a tailnet only.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"time"

	"github.com/JakeMalmrose/draupforge/protocol"
	fm "github.com/JakeMalmrose/draupforge/sim/fixmath"
	"github.com/JakeMalmrose/draupforge/sim/space"
)

const (
	// tickRateWindow is how many recent tick wall-times feed the actual-rate
	// gauge (~4s at 30Hz).
	tickRateWindow = 121
	// adminEventCap bounds the recent-events ring served at /api/status.
	adminEventCap = 200
	// adminOpTimeout is how long a handler waits for the tick goroutine. A
	// healthy loop replies within one tick; hitting this means it's wedged.
	adminOpTimeout = 2 * time.Second
)

// adminEvent is a wire event stamped with the tick it happened on.
type adminEvent struct {
	Tick uint64 `json:"tick"`
	protocol.EventSnap
}

type adminClientInfo struct {
	Actor     uint64 `json:"actor"`
	Mode      string `json:"mode"`
	BytesSent uint64 `json:"bytes_sent"`
}

type adminStatus struct {
	Paused       bool              `json:"paused"`
	Tick         uint64            `json:"tick"`
	TickHzTarget float64           `json:"tick_hz_target"`
	TickHzActual float64           `json:"tick_hz_actual"`
	SendEvery    int               `json:"send_every"`
	ProtocolV    int               `json:"protocol_version"`
	Actors       int               `json:"actors"`
	Projectiles  int               `json:"projectiles"`
	Drops        int               `json:"drops"`
	Clients      []adminClientInfo `json:"clients"`
	// WorldHash travels as hex text — a uint64 in JSON loses precision past
	// 2^53 in JS.
	WorldHash string `json:"world_hash"`
	ActorDefs    []string          `json:"actor_defs"`
	Events       []adminEvent      `json:"events"`
}

// saveBlob carries a serialized world from the tick goroutine to the admin
// handler that writes it out.
type saveBlob struct {
	data []byte
	tick uint64
}

// runOnTick hands fn to the tick goroutine and waits for its result.
func (in *Instance) runOnTick(fn func() (any, error)) (any, error) {
	op := adminOp{fn: fn, reply: make(chan adminReply, 1)}
	in.mu.Lock()
	in.adminOps = append(in.adminOps, op)
	in.mu.Unlock()
	select {
	case r := <-op.reply:
		return r.v, r.err
	case <-time.After(adminOpTimeout):
		return nil, errors.New("tick loop unresponsive")
	}
}

func (m mode) String() string {
	switch m {
	case modeBinary:
		return "binary"
	case modeJSONView:
		return "json-view"
	default:
		return "json-world"
	}
}

func (in *Instance) adminStatusLocked() *adminStatus {
	w := in.sim.W
	st := &adminStatus{
		Paused:       in.paused,
		Tick:         w.Tick,
		TickHzTarget: float64(time.Second) / float64(in.cfg.TickInterval),
		SendEvery:    in.cfg.SendEvery,
		ProtocolV:    protocol.Version,
		Actors:       len(w.Actors),
		Projectiles:  len(w.Projectiles),
		Drops:        len(w.Drops),
		WorldHash:    fmt.Sprintf("%016x", w.Hash()),
		Events:       append([]adminEvent(nil), in.recentEvents...),
	}
	if n := len(in.tickTimes); n >= 2 {
		span := in.tickTimes[n-1].Sub(in.tickTimes[0]).Seconds()
		if span > 0 {
			st.TickHzActual = float64(n-1) / span
		}
	}
	for _, c := range in.clients {
		st.Clients = append(st.Clients, adminClientInfo{
			Actor: uint64(c.actor), Mode: c.mode.String(), BytesSent: c.bytesSent,
		})
	}
	for id := range w.Content.Actors {
		st.ActorDefs = append(st.ActorDefs, id)
	}
	sort.Strings(st.ActorDefs)
	return st
}

func (in *Instance) serveAdmin(ctx context.Context) {
	srv := &http.Server{Addr: in.cfg.AdminAddr, Handler: in.adminMux()}
	go func() {
		<-ctx.Done()
		srv.Close()
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("server: admin listener: %v", err)
	}
}

func (in *Instance) adminMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		v, err := in.runOnTick(func() (any, error) { return in.adminStatusLocked(), nil })
		adminReplyJSON(w, v, err)
	})
	mux.HandleFunc("POST /api/pause", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Paused bool `json:"paused"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		v, err := in.runOnTick(func() (any, error) {
			in.pauseDesired = req.Paused
			return map[string]bool{"paused": req.Paused}, nil
		})
		adminReplyJSON(w, v, err)
	})
	mux.HandleFunc("POST /api/spawn", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Def  string `json:"def"`
			X, Y int64  // milli-units, like the wire
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		v, err := in.runOnTick(func() (any, error) {
			id, err := in.sim.Spawn(req.Def, space.V(fm.FromMilli(req.X), fm.FromMilli(req.Y)))
			if err != nil {
				return nil, err
			}
			return map[string]uint64{"id": uint64(id)}, nil
		})
		adminReplyJSON(w, v, err)
	})
	mux.HandleFunc("POST /api/save", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Serialize on the tick goroutine (a consistent between-ticks view);
		// write the file here so disk latency never stalls the tick loop.
		v, err := in.runOnTick(func() (any, error) {
			data, err := in.sim.W.Save()
			if err != nil {
				return nil, err
			}
			if in.run > 0 {
				// Descent instances save the run envelope so -load resumes
				// mid-run; plain arenas keep the bare world format.
				if data, err = in.encodeRunSave(data); err != nil {
					return nil, err
				}
			}
			return saveBlob{data: data, tick: in.sim.W.Tick}, nil
		})
		if err != nil {
			adminReplyJSON(w, nil, err)
			return
		}
		blob := v.(saveBlob)
		path := req.Path
		if path == "" {
			path = fmt.Sprintf("draupforge-save-tick%d.json", blob.tick)
		}
		if err := os.WriteFile(path, blob.data, 0o644); err != nil {
			adminReplyJSON(w, nil, err)
			return
		}
		adminReplyJSON(w, map[string]any{
			"path": path, "tick": blob.tick, "bytes": len(blob.data),
		}, nil)
	})
	mux.HandleFunc("POST /api/kick", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Actor uint64 `json:"actor"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		v, err := in.runOnTick(func() (any, error) {
			for _, c := range in.clients {
				if uint64(c.actor) == req.Actor {
					c.tr.Close() // readLoop files the leave
					return map[string]bool{"kicked": true}, nil
				}
			}
			return nil, errors.New("no client with that actor")
		})
		adminReplyJSON(w, v, err)
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(adminPage))
	})
	return mux
}

func adminReplyJSON(w http.ResponseWriter, v any, err error) {
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// adminPage is the whole dashboard — observe, pause/resume, spawn, kick. It
// only talks to /api/*; keeping it embedded means the admin port works
// wherever the binary runs, no static dir needed. Not pretty on purpose.
const adminPage = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>draupforge admin</title>
<style>
  body { background: #0b0b10; color: #cfc9bf; font: 13px/1.5 monospace; margin: 2em auto; max-width: 64em; padding: 0 1em; }
  h1 { font-size: 15px; color: #b8a44a; }
  h2 { font-size: 13px; color: #8d8678; margin: 1.2em 0 .3em; }
  table { border-collapse: collapse; }
  td, th { padding: .15em .9em .15em 0; text-align: left; font-weight: normal; }
  th { color: #8d8678; }
  button { background: #1c1c26; color: #cfc9bf; border: 1px solid #3a3a4a; padding: .2em .8em; font: inherit; cursor: pointer; }
  button:hover { border-color: #b8a44a; }
  input, select { background: #15151d; color: #cfc9bf; border: 1px solid #3a3a4a; font: inherit; padding: .15em .3em; }
  #events { height: 14em; overflow-y: auto; background: #08080c; border: 1px solid #1c1c26; padding: .5em; white-space: pre; }
  .paused { color: #d35400; }
  .err { color: #a32626; }
</style>
</head>
<body>
<h1>draupforge admin</h1>
<p>
  <button id="pausebtn">…</button>
  <span id="tickline"></span>
</p>
<h2>world</h2>
<table><tbody id="world"></tbody></table>
<h2>spawn</h2>
<p>
  <select id="def"></select>
  x <input id="sx" value="0" size="5"> y <input id="sy" value="0" size="5"> (units)
  <button id="spawnbtn">spawn</button>
  <span id="spawnmsg"></span>
</p>
<h2>save</h2>
<p>
  path <input id="savepath" placeholder="(default: draupforge-save-tick&lt;N&gt;.json)" size="34">
  <button id="savebtn">save world</button>
  <span id="savemsg"></span>
  — load with: <code>cmd/server -load &lt;path&gt;</code>
</p>
<h2>clients</h2>
<table>
  <thead><tr><th>actor</th><th>mode</th><th>sent</th><th>rate</th><th></th></tr></thead>
  <tbody id="clients"></tbody>
</table>
<h2>events</h2>
<div id="events"></div>
<script>
"use strict";
let st = null;
const prevBytes = new Map(); // actor -> {bytes, at} for the rate column

async function api(path, body) {
  const opts = body ? { method: "POST", body: JSON.stringify(body) } : {};
  const res = await fetch(path, opts);
  if (!res.ok) throw new Error(await res.text());
  return res.json();
}

function fmtBytes(n) {
  if (n > 1 << 20) return (n / (1 << 20)).toFixed(1) + " MiB";
  if (n > 1 << 10) return (n / (1 << 10)).toFixed(1) + " KiB";
  return n + " B";
}

function render() {
  document.getElementById("pausebtn").textContent = st.paused ? "resume" : "pause";
  document.getElementById("tickline").innerHTML =
    "tick " + st.tick +
    " · " + st.tick_hz_actual.toFixed(1) + "/" + st.tick_hz_target.toFixed(0) + " Hz" +
    (st.paused ? ' · <span class="paused">PAUSED</span>' : "");

  document.getElementById("world").innerHTML =
    "<tr><th>actors</th><td>" + st.actors + "</td></tr>" +
    "<tr><th>projectiles</th><td>" + st.projectiles + "</td></tr>" +
    "<tr><th>drops</th><td>" + st.drops + "</td></tr>" +
    "<tr><th>state hash</th><td>" + st.world_hash + "</td></tr>" +
    "<tr><th>protocol</th><td>v" + st.protocol_version + ", views every " + st.send_every + " ticks</td></tr>";

  const def = document.getElementById("def");
  if (def.options.length === 0) {
    for (const d of st.actor_defs) def.add(new Option(d));
  }

  const now = performance.now();
  const rows = (st.clients || []).map((c) => {
    const prev = prevBytes.get(c.actor);
    let rate = "";
    if (prev && now > prev.at) {
      rate = fmtBytes(((c.bytes_sent - prev.bytes) * 1000) / (now - prev.at)) + "/s";
    }
    prevBytes.set(c.actor, { bytes: c.bytes_sent, at: now });
    return "<tr><td>" + c.actor + "</td><td>" + c.mode + "</td><td>" + fmtBytes(c.bytes_sent) +
      '</td><td>' + rate + '</td><td><button onclick="kick(' + c.actor + ')">kick</button></td></tr>';
  });
  document.getElementById("clients").innerHTML = rows.join("") || "<tr><td>none</td></tr>";

  const ev = document.getElementById("events");
  const stick = ev.scrollTop + ev.clientHeight >= ev.scrollHeight - 4;
  ev.textContent = (st.events || []).map((e) =>
    e.tick + "  " + e.kind +
    (e.actor ? " actor=" + e.actor : "") + (e.other ? " other=" + e.other : "") +
    (e.amount ? " amount=" + e.amount / 1000 : "") + (e.note ? " (" + e.note + ")" : "")
  ).join("\n");
  if (stick) ev.scrollTop = ev.scrollHeight;
}

async function poll() {
  try {
    st = await api("/api/status");
    render();
  } catch (e) {
    document.getElementById("tickline").innerHTML = '<span class="err">' + e.message + "</span>";
  }
}

document.getElementById("pausebtn").onclick = async () => {
  await api("/api/pause", { paused: !st.paused });
  poll();
};
document.getElementById("spawnbtn").onclick = async () => {
  const msg = document.getElementById("spawnmsg");
  try {
    const r = await api("/api/spawn", {
      def: document.getElementById("def").value,
      X: Math.round(parseFloat(document.getElementById("sx").value || "0") * 1000),
      Y: Math.round(parseFloat(document.getElementById("sy").value || "0") * 1000),
    });
    msg.textContent = "spawned #" + r.id;
  } catch (e) {
    msg.innerHTML = '<span class="err">' + e.message + "</span>";
  }
  poll();
};
document.getElementById("savebtn").onclick = async () => {
  const msg = document.getElementById("savemsg");
  try {
    const r = await api("/api/save", { path: document.getElementById("savepath").value || "" });
    msg.textContent = "wrote " + r.path + " (tick " + r.tick + ", " + fmtBytes(r.bytes) + ")";
  } catch (e) {
    msg.innerHTML = '<span class="err">' + e.message + "</span>";
  }
};
window.kick = async (actor) => { await api("/api/kick", { actor }); poll(); };

poll();
setInterval(poll, 1000);
</script>
</body>
</html>
`
