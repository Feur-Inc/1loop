// SPDX-License-Identifier: MPL-2.0

// Package discord shows what 1loop is doing on the user's Discord profile
// (Rich Presence), through the Discord app's local IPC socket. Nothing goes
// to Discord's servers from here: the Discord app running on the same
// computer publishes it, under the application whose ID is given, and only
// while that app runs.
//
// The protocol is a frame of op code and length (two little-endian uint32)
// then that many bytes of JSON: a handshake with the application ID, the
// READY answer, then SET_ACTIVITY commands, each answered.
package discord

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Activity is what the profile shows: "Playing <application name>", then
// these lines, the large image with its tooltip, and the time elapsed since
// Start.
type Activity struct {
	Details    string
	State      string
	LargeImage string // an asset key of the application, or an https URL (GIFs play)
	LargeText  string
	SmallImage string
	SmallText  string
	Start      time.Time
	Buttons    []Button // shown to the others, at most two
}

// Button is a link under the activity.
type Button struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

const (
	opHandshake = 0
	opFrame     = 1
	opClose     = 2
	opPing      = 3
	opPong      = 4
)

// Discord takes five activity updates in 20 seconds and drops the others:
// the latest wins, at most one every minGap.
const (
	minGap     = 4 * time.Second
	retryAfter = 15 * time.Second // Discord not running, or gone
	replyWait  = 5 * time.Second
)

// Presence keeps the profile's activity in step with the last Set, in the
// background: it connects when there is something to show, waits for
// Discord when it is not running and reconnects when it restarts.
type Presence struct {
	appID string
	logf  func(format string, args ...any)
	wake  chan struct{}

	mu     sync.Mutex
	want   *Activity
	closed bool
}

// New starts a presence for a Discord application, telling logf how its
// connection goes. With no ID it does nothing.
func New(appID string, logf func(format string, args ...any)) *Presence {
	presence := &Presence{appID: appID, logf: logf, wake: make(chan struct{}, 1)}
	if appID != "" {
		go presence.run()
	}
	return presence
}

// Set shows an activity; nil clears it and lets go of Discord.
func (presence *Presence) Set(activity *Activity) {
	if presence == nil {
		return
	}
	presence.mu.Lock()
	presence.want = activity
	presence.mu.Unlock()
	presence.poke()
}

// Close clears the activity and stops.
func (presence *Presence) Close() {
	if presence == nil {
		return
	}
	presence.mu.Lock()
	presence.want, presence.closed = nil, true
	presence.mu.Unlock()
	presence.poke()
}

func (presence *Presence) poke() {
	select {
	case presence.wake <- struct{}{}:
	default:
	}
}

func (presence *Presence) run() {
	var (
		conn     *conn
		sent     *Activity
		lastSend time.Time
		failed   time.Time
		problem  string // the last one logged: Discord not running is logged once
	)
	drop := func() {
		if conn != nil {
			conn.close()
			conn = nil
		}
		sent = nil
	}
	for {
		presence.mu.Lock()
		want, closed := presence.want, presence.closed
		presence.mu.Unlock()
		var wait time.Duration // 0: wait for the next Set
		switch {
		case want == nil && conn != nil:
			// Clearing first is what makes the profile drop it at once.
			_ = conn.setActivity(nil)
			drop()
		case want == nil || sameActivity(want, sent):
		case time.Since(failed) < retryAfter:
			wait = retryAfter - time.Since(failed)
		case time.Since(lastSend) < minGap:
			wait = minGap - time.Since(lastSend)
		default:
			if conn == nil {
				var err error
				if conn, err = connect(presence.appID); err != nil {
					if err.Error() != problem {
						presence.logf("not connected: %v", err)
						problem = err.Error()
					}
					failed = time.Now()
					wait = retryAfter
					break
				}
				presence.logf("connected")
				problem = ""
			}
			lastSend = time.Now()
			if err := conn.setActivity(want); err != nil {
				presence.logf("activity not set: %v", err)
				drop()
				failed = time.Now()
				wait = retryAfter
				break
			}
			sent = want
		}
		if closed {
			drop()
			return
		}
		var gone <-chan struct{} // Discord quit or restarted: show it again
		if conn != nil {
			gone = conn.done
		}
		var timer <-chan time.Time
		if wait != 0 {
			timer = time.After(wait)
		}
		select {
		case <-presence.wake:
		case <-timer:
		case <-gone:
			presence.logf("disconnected")
			drop()
		}
	}
}

func sameActivity(a, b *Activity) bool {
	return a != nil && b != nil && reflect.DeepEqual(*a, *b)
}

// frame is one message of the protocol.
type frame struct {
	op   uint32
	body []byte
}

// conn is a connection to the Discord app that went through the handshake.
type conn struct {
	rw      io.ReadWriteCloser
	frames  chan frame // read in the background: Windows pipes have no deadlines
	done    chan struct{}
	nonce   int
	closeMu sync.Once
}

// dialer is dial, but for the tests' fake Discord.
var dialer = dial

func connect(appID string) (*conn, error) {
	rw, err := dialer()
	if err != nil {
		return nil, err
	}
	c := &conn{rw: rw, frames: make(chan frame, 8), done: make(chan struct{})}
	go c.read()
	if err := c.write(opHandshake, map[string]any{"v": 1, "client_id": appID}); err != nil {
		c.close()
		return nil, err
	}
	reply, err := c.reply("")
	if err != nil {
		c.close()
		return nil, err
	}
	if reply.Evt != "READY" {
		c.close()
		return nil, fmt.Errorf("handshake: %s %s", reply.Evt, reply.Data)
	}
	return c, nil
}

func (c *conn) read() {
	defer close(c.done)
	var header [8]byte
	for {
		if _, err := io.ReadFull(c.rw, header[:]); err != nil {
			return
		}
		op, length := binary.LittleEndian.Uint32(header[:4]), binary.LittleEndian.Uint32(header[4:])
		if length > 1<<20 {
			return
		}
		body := make([]byte, length)
		if _, err := io.ReadFull(c.rw, body); err != nil {
			return
		}
		if op == opPing { // answered aside, so that reading never waits on a write
			go func() { _ = c.writeRaw(opPong, body) }()
			continue
		}
		select {
		case c.frames <- frame{op, body}:
		default: // an event nobody waits for
		}
	}
}

func (c *conn) write(op uint32, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return c.writeRaw(op, body)
}

func (c *conn) writeRaw(op uint32, body []byte) error {
	message := make([]byte, 8+len(body))
	binary.LittleEndian.PutUint32(message[:4], op)
	binary.LittleEndian.PutUint32(message[4:8], uint32(len(body)))
	copy(message[8:], body)
	_, err := c.rw.Write(message)
	return err
}

type reply struct {
	Cmd   string          `json:"cmd"`
	Evt   string          `json:"evt"`
	Nonce string          `json:"nonce"`
	Data  json.RawMessage `json:"data"`
}

// reply waits for the answer to the command sent with nonce ("" for the
// handshake's READY).
func (c *conn) reply(nonce string) (reply, error) {
	timeout := time.After(replyWait)
	for {
		select {
		case <-c.done:
			return reply{}, errors.New("Discord closed the connection")
		case got := <-c.frames:
			var answer reply
			if got.op == opClose {
				return reply{}, fmt.Errorf("Discord closed the connection: %s", got.body)
			}
			if err := json.Unmarshal(got.body, &answer); err != nil {
				return reply{}, err
			}
			if answer.Nonce == nonce || (nonce == "" && answer.Evt == "READY") {
				return answer, nil
			}
		case <-timeout:
			return reply{}, errors.New("no answer from Discord")
		}
	}
}

func (c *conn) setActivity(activity *Activity) error {
	c.nonce++
	nonce := strconv.Itoa(os.Getpid()) + "-" + strconv.Itoa(c.nonce)
	args := map[string]any{"pid": os.Getpid()}
	if activity != nil {
		args["activity"] = activity.payload()
	}
	if err := c.write(opFrame, map[string]any{"cmd": "SET_ACTIVITY", "args": args, "nonce": nonce}); err != nil {
		return err
	}
	answer, err := c.reply(nonce)
	if err != nil {
		return err
	}
	if answer.Evt == "ERROR" {
		return fmt.Errorf("Discord refused it: %s", answer.Data)
	}
	return nil
}

func (c *conn) close() {
	c.closeMu.Do(func() {
		_ = c.writeRaw(opClose, []byte("{}"))
		_ = c.rw.Close()
	})
}

// payload is the activity as Discord takes it. Its texts have 2 to 128
// characters: shorter ones are left out, longer ones cut.
func (activity *Activity) payload() map[string]any {
	out := map[string]any{"type": 0} // "Playing"
	put := func(into map[string]any, key, value string) {
		if value = clip(value, 128); utf8.RuneCountInString(value) >= 2 {
			into[key] = value
		}
	}
	put(out, "details", activity.Details)
	put(out, "state", activity.State)
	if !activity.Start.IsZero() {
		out["timestamps"] = map[string]any{"start": activity.Start.UnixMilli()}
	}
	assets := map[string]any{}
	if activity.LargeImage != "" {
		assets["large_image"] = activity.LargeImage
		put(assets, "large_text", activity.LargeText)
	}
	if activity.SmallImage != "" {
		assets["small_image"] = activity.SmallImage
		put(assets, "small_text", activity.SmallText)
	}
	if len(assets) != 0 {
		out["assets"] = assets
	}
	var buttons []Button
	for _, button := range activity.Buttons {
		if len(buttons) < 2 && button.Label != "" && strings.HasPrefix(button.URL, "https://") {
			buttons = append(buttons, Button{clip(button.Label, 32), button.URL})
		}
	}
	if len(buttons) != 0 {
		out["buttons"] = buttons
	}
	return out
}

// clip cuts text to at most limit characters, with an ellipsis.
func clip(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return strings.TrimSpace(string(runes[:limit-1])) + "…"
}
