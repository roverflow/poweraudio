package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/roverflow/poweraudio/internal/config"
)

const (
	// dialTimeout covers a socket file that exists but has nothing behind it.
	dialTimeout = 2 * time.Second

	// callTimeout bounds a single request. Without it a wedged daemon hung
	// the UI's refresh forever instead of showing that it is offline.
	callTimeout = 5 * time.Second

	// maxLine is the largest response a client will read. A snapshot with
	// 200 events and a long device list is well under 100 KB.
	maxLine = 1 << 20
)

type Client struct {
	socketPath string
}

func NewClient(socketPath string) *Client {
	return &Client{socketPath: socketPath}
}

// call sends one request and reads one response. Every method except
// Subscribe goes through here.
func (c *Client) call(method string, params any, out any) error {
	conn, err := net.DialTimeout("unix", c.socketPath, dialTimeout)
	if err != nil {
		return fmt.Errorf("connecting to daemon: %w", err)
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(callTimeout)); err != nil {
		return fmt.Errorf("setting deadline: %w", err)
	}
	if err := writeRequest(conn, method, params); err != nil {
		return err
	}

	scanner := newScanner(conn)
	if !scanner.Scan() {
		return fmt.Errorf("no response from daemon")
	}
	return decodeResponse(scanner.Bytes(), out)
}

func writeRequest(conn net.Conn, method string, params any) error {
	req := Request{Method: method}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("encoding params: %w", err)
		}
		req.Params = raw
	}
	data, _ := json.Marshal(req)
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("sending request: %w", err)
	}
	return nil
}

func newScanner(conn net.Conn) *bufio.Scanner {
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLine)
	return scanner
}

// decodeResponse unwraps the envelope. A daemon-side error becomes a Go
// error; a payload is decoded into out when the caller wants one.
func decodeResponse(line []byte, out any) error {
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return fmt.Errorf("parsing response: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}
	if out == nil || len(resp.Data) == 0 {
		return nil
	}
	if err := json.Unmarshal(resp.Data, out); err != nil {
		return fmt.Errorf("parsing response data: %w", err)
	}
	return nil
}

// Snapshot is one round trip for everything a screen or a status bar needs.
func (c *Client) Snapshot() (*Snapshot, error) {
	var snap Snapshot
	if err := c.call(MethodSnapshot, nil, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

// Subscribe opens a connection the daemon keeps writing to. The first
// Snapshot arrives right away; another follows every time devices, events
// or config change. The channel closes when ctx ends or the daemon goes
// away, so a closed channel means "reconnect or show offline".
func (c *Client) Subscribe(ctx context.Context) (<-chan Snapshot, error) {
	conn, err := net.DialTimeout("unix", c.socketPath, dialTimeout)
	if err != nil {
		return nil, fmt.Errorf("connecting to daemon: %w", err)
	}
	if err := writeRequest(conn, MethodSubscribe, nil); err != nil {
		conn.Close()
		return nil, err
	}

	ch := make(chan Snapshot, 4)
	go func() {
		defer close(ch)
		defer conn.Close()

		// Closing the socket is what unblocks the scanner when ctx ends.
		stop := context.AfterFunc(ctx, func() { conn.Close() })
		defer stop()

		scanner := newScanner(conn)
		for scanner.Scan() {
			var snap Snapshot
			if err := decodeResponse(scanner.Bytes(), &snap); err != nil {
				return
			}
			select {
			case ch <- snap:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

func (c *Client) SetDefault(deviceID string) error {
	return c.call(MethodSetDefault, SetDefaultParams{DeviceID: deviceID}, nil)
}

func (c *Client) SetVolume(deviceID string, percent int) error {
	return c.call(MethodSetVolume, SetVolumeParams{DeviceID: deviceID, Percent: percent}, nil)
}

func (c *Client) ToggleMute(deviceID string) error {
	return c.call(MethodToggleMute, ToggleMuteParams{DeviceID: deviceID}, nil)
}

func (c *Client) UpdatePriorities(priorities []config.PriorityEntry) error {
	if priorities == nil {
		priorities = []config.PriorityEntry{}
	}
	return c.call(MethodUpdatePriorities, priorities, nil)
}

func (c *Client) UpdateSwitching(onConnect, onDisconnect string) error {
	return c.call(MethodUpdateSwitching, UpdateSwitchingParams{
		OnConnect:    onConnect,
		OnDisconnect: onDisconnect,
	}, nil)
}

// ReloadConfig asks the daemon to re-read its config file.
func (c *Client) ReloadConfig() error {
	return c.call(MethodReloadConfig, nil, nil)
}

// Ping reports whether a daemon answers on the socket.
func (c *Client) Ping() bool {
	_, err := c.Snapshot()
	return err == nil
}
