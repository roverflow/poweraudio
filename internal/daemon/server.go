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
	// requestTimeout bounds a single client exchange. A client that connects
	// and then says nothing used to hold a goroutine for the life of the
	// daemon. A subscription is exempt: it is idle by design.
	requestTimeout = 10 * time.Second

	// maxRequest matches the client's read limit. A priority list long enough
	// to exceed the default scanner buffer used to look like a client that
	// sent nothing at all.
	maxRequest = 1 << 20
)

// ErrAlreadyRunning means a live daemon answered on the socket, so this one
// has nothing to do. main.go logs it and exits zero on purpose: the unit is
// Restart=on-failure with RestartSec=5, so a non-zero exit here had systemd
// restarting the service every five seconds for as long as a session daemon
// held the socket, and at that spacing the start limit of five failures in ten
// seconds never trips to stop it.
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
	// Unlinking the socket unconditionally let a second daemon take the first
	// one's place, after which both fought over the default sink. Ask first:
	// anything that answers is a live daemon, anything that does not is a
	// leftover file from a crash.
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

// stream keeps the connection open and writes a snapshot per change until the
// client hangs up or the daemon stops.
func (s *Server) stream(ctx context.Context, conn net.Conn) {
	// No request deadline: a subscription spends most of its life waiting for
	// something to happen.
	_ = conn.SetDeadline(time.Time{})

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// The client has nothing more to say, so reading is only how the server
	// learns it went away. Without this a closed UI left a subscription
	// running until the next snapshot failed to write.
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
