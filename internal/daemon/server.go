package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"github.com/roverflow/poweraudio/internal/ipc"
)

const (
	// requestTimeout stops a silent client holding a goroutine forever. A
	// subscription is exempt.
	requestTimeout = 10 * time.Second

	// maxRequest matches the client's read limit, for long priority lists.
	maxRequest = 1 << 20
)

// ErrAlreadyRunning means a live daemon answered on the socket. main.go exits
// zero on it, since a non-zero exit makes systemd's Restart=on-failure restart
// the unit every five seconds.
var ErrAlreadyRunning = errors.New("another poweraudio daemon is already listening")

type Server struct {
	socketPath string
	daemon     *Daemon
	listener   net.Listener
}

func NewServer(socketPath string, d *Daemon) *Server {
	return &Server{
		socketPath: socketPath,
		daemon:     d,
	}
}

func (s *Server) Start(ctx context.Context) error {
	// Removing the socket blindly would let two daemons fight over the
	// default sink. A dial that connects means a live daemon. Otherwise the
	// file is stale.
	if conn, err := net.DialTimeout("unix", s.socketPath, time.Second); err == nil {
		conn.Close()
		return fmt.Errorf("%w on %s", ErrAlreadyRunning, s.socketPath)
	}
	os.Remove(s.socketPath)

	ln, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return err
	}
	s.listener = ln

	if err := os.Chmod(s.socketPath, 0o600); err != nil {
		ln.Close()
		return err
	}

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("accept error: %v", err)
				continue
			}
			go s.handleConn(ctx, conn)
		}
	}()

	return nil
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(requestTimeout))

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), maxRequest)
	if !scanner.Scan() {
		return
	}

	var req ipc.Request
	if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
		writeResponse(conn, ipc.ErrorResponse("invalid request: "+err.Error()))
		return
	}

	if req.Method == ipc.MethodSubscribe {
		s.stream(ctx, conn)
		return
	}

	writeResponse(conn, s.daemon.Handle(ctx, req))
}

func (s *Server) stream(ctx context.Context, conn net.Conn) {
	// A subscription waits indefinitely, so it has no request deadline.
	_ = conn.SetDeadline(time.Time{})

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Reading is how the server notices the client went away. Without it, a
	// closed UI keeps its subscription until the next write fails.
	go func() {
		defer cancel()
		buf := make([]byte, 256)
		for {
			if _, err := conn.Read(buf); err != nil {
				return
			}
		}
	}()

	for snap := range s.daemon.Subscribe(ctx) {
		// A client that stopped reading must not hold a write open forever.
		_ = conn.SetWriteDeadline(time.Now().Add(requestTimeout))
		if err := writeResponse(conn, ipc.SuccessResponse(snap)); err != nil {
			return
		}
	}
}

func writeResponse(conn net.Conn, resp ipc.Response) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = conn.Write(data)
	return err
}

func (s *Server) Close() {
	if s.listener != nil {
		s.listener.Close()
	}
	os.Remove(s.socketPath)
}
