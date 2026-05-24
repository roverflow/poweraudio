package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"log"
	"net"
	"os"

	"github.com/roverflow/poweraudio/internal/ipc"
)

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

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		return
	}

	var req ipc.Request
	if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
		resp := ipc.ErrorResponse("invalid request: " + err.Error())
		data, _ := json.Marshal(resp)
		data = append(data, '\n')
		conn.Write(data)
		return
	}

	respCh := make(chan ipc.Response, 1)
	ipcReq := IPCRequest{Request: req, Response: respCh}

	select {
	case s.daemon.IPCChannel() <- ipcReq:
	case <-ctx.Done():
		return
	}

	select {
	case resp := <-respCh:
		data, _ := json.Marshal(resp)
		data = append(data, '\n')
		conn.Write(data)
	case <-ctx.Done():
	}
}

func (s *Server) Close() {
	if s.listener != nil {
		s.listener.Close()
	}
	os.Remove(s.socketPath)
}
