package ipc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"

	"github.com/roverflow/poweraudio/internal/audio"
	"github.com/roverflow/poweraudio/internal/config"
)

type Client struct {
	socketPath string
}

func NewClient(socketPath string) *Client {
	return &Client{socketPath: socketPath}
}

func (c *Client) call(req Request) (Response, error) {
	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return Response{}, fmt.Errorf("connecting to daemon: %w", err)
	}
	defer conn.Close()

	data, _ := json.Marshal(req)
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		return Response{}, fmt.Errorf("sending request: %w", err)
	}

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		return Response{}, fmt.Errorf("no response from daemon")
	}

	var resp Response
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return Response{}, fmt.Errorf("parsing response: %w", err)
	}
	return resp, nil
}

func (c *Client) ListDevices() ([]audio.Device, error) {
	resp, err := c.call(Request{Method: MethodListDevices})
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("daemon error: %s", resp.Error)
	}
	var devices []audio.Device
	if err := json.Unmarshal(resp.Data, &devices); err != nil {
		return nil, err
	}
	return devices, nil
}

func (c *Client) GetDefault() (*audio.Device, error) {
	resp, err := c.call(Request{Method: MethodGetDefault})
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("daemon error: %s", resp.Error)
	}
	var dev audio.Device
	if err := json.Unmarshal(resp.Data, &dev); err != nil {
		return nil, err
	}
	return &dev, nil
}

func (c *Client) SetDefault(deviceID string) error {
	params, _ := json.Marshal(SetDefaultParams{DeviceID: deviceID})
	resp, err := c.call(Request{Method: MethodSetDefault, Params: params})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}
	return nil
}

func (c *Client) GetStatus() (*StatusData, error) {
	resp, err := c.call(Request{Method: MethodGetStatus})
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("daemon error: %s", resp.Error)
	}
	var status StatusData
	if err := json.Unmarshal(resp.Data, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

func (c *Client) GetEvents(limit int) ([]EventLog, error) {
	params, _ := json.Marshal(GetEventsParams{Limit: limit})
	resp, err := c.call(Request{Method: MethodGetEvents, Params: params})
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("daemon error: %s", resp.Error)
	}
	var events []EventLog
	if err := json.Unmarshal(resp.Data, &events); err != nil {
		return nil, err
	}
	return events, nil
}

func (c *Client) GetConfig() (*config.Config, error) {
	resp, err := c.call(Request{Method: MethodGetConfig})
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("daemon error: %s", resp.Error)
	}
	var cfg config.Config
	if err := json.Unmarshal(resp.Data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Client) SetVolume(deviceID string, percent int) error {
	params, _ := json.Marshal(SetVolumeParams{DeviceID: deviceID, Percent: percent})
	resp, err := c.call(Request{Method: MethodSetVolume, Params: params})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}
	return nil
}

func (c *Client) ToggleMute(deviceID string) error {
	params, _ := json.Marshal(ToggleMuteParams{DeviceID: deviceID})
	resp, err := c.call(Request{Method: MethodToggleMute, Params: params})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}
	return nil
}

func (c *Client) UpdatePriorities(priorities []config.PriorityEntry) error {
	params, _ := json.Marshal(priorities)
	resp, err := c.call(Request{Method: MethodUpdatePriorities, Params: params})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}
	return nil
}

func (c *Client) UpdateSwitching(onConnect, onDisconnect string) error {
	params, _ := json.Marshal(UpdateSwitchingParams{
		OnConnect:    onConnect,
		OnDisconnect: onDisconnect,
	})
	resp, err := c.call(Request{Method: MethodUpdateSwitching, Params: params})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("daemon error: %s", resp.Error)
	}
	return nil
}

func (c *Client) Ping() bool {
	_, err := c.GetStatus()
	return err == nil
}
