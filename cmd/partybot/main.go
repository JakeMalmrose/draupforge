// Command partybot is a fake friend for multiplayer testing: it claims a
// name, connects, idles in its own world, and auto-accepts any party
// invite. Point a real client at the same server and invite it.
//
//	go run ./cmd/partybot -url http://localhost:8080 -name Botty
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"

	"github.com/coder/websocket"

	"github.com/JakeMalmrose/draupforge/protocol"
)

func main() {
	base := flag.String("url", "http://localhost:8080", "server base URL")
	name := flag.String("name", "Botty", "name to claim (or resume)")
	flag.Parse()

	jar, _ := cookiejar.New(nil)
	hc := &http.Client{Jar: jar}

	body, _ := json.Marshal(map[string]string{"name": *name})
	resp, err := hc.Post(*base+"/api/claim", "application/json", bytes.NewReader(body))
	if err != nil {
		fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusConflict {
		fatal(fmt.Errorf("claim: status %d", resp.StatusCode))
	}
	// 409 = name exists; without its cookie we can't be them. Claims are
	// per-jar here, so just pick a fresh name in that case.
	if resp.StatusCode == http.StatusConflict {
		fatal(fmt.Errorf("name %q is taken — pick another with -name", *name))
	}

	wsURL := "ws" + strings.TrimPrefix(*base, "http") + "/ws"
	ws, _, err := websocket.Dial(context.Background(), wsURL, &websocket.DialOptions{HTTPClient: hc})
	if err != nil {
		fatal(err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "bye")
	ws.SetReadLimit(1 << 22) // views on a busy floor outgrow the default 32K
	fmt.Printf("partybot %q connected to %s — waiting for invites\n", *name, *base)

	for {
		kind, data, err := ws.Read(context.Background())
		if err != nil {
			fatal(err)
		}
		if kind != websocket.MessageText {
			continue // binary views; the bot has no eyes
		}
		var msg protocol.ServerMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		switch msg.Type {
		case "welcome":
			fmt.Printf("welcome: gen %d, actor %d, %d in roster\n", msg.Gen, msg.Actor, len(msg.Roster))
		case "social":
			if msg.Social != nil && msg.Social.Invite != "" {
				fmt.Printf("invited by %s — accepting\n", msg.Social.Invite)
				accept, _ := json.Marshal(protocol.Command{Kind: "accept_invite"})
				if err := ws.Write(context.Background(), websocket.MessageText, accept); err != nil {
					fatal(err)
				}
			}
		case "error":
			fatal(fmt.Errorf("server refused: %s", msg.Error))
		}
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "partybot:", err)
	os.Exit(1)
}
