package tui

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/roverflow/poweraudio/internal/config"
	"github.com/roverflow/poweraudio/internal/ipc"
)

// subRetryDelay is how long the UI waits before dialling again after the
// subscription drops. The daemon restarting under systemd is back inside a
// second or two, and a tighter loop just burns a connect attempt per frame.
const subRetryDelay = time.Second

var errNoDaemon = errors.New("no daemon connection")

// The gen field on every subscription message is the generation of the
// subscription that produced it. A message from an abandoned connection, or a
// retry timer that a manual reconnect has already overtaken, carries an older
// generation and is dropped rather than resurrecting a dead channel.
type (
	subReadyMsg struct {
		gen int
		ch  <-chan ipc.Snapshot
	}

	subFailedMsg struct {
		gen int
		err error
	}

	subClosedMsg struct{ gen int }

	subRetryMsg struct{ gen int }

	snapshotMsg struct {
		gen  int
		snap ipc.Snapshot
	}

	// refreshMsg is the answer to a manual r, which goes out on its own
	// connection and does not disturb the subscription.
	refreshMsg struct {
		snap *ipc.Snapshot
		err  error
	}
)

// Requests raised by a screen. Screens hold no client, so they ask for an
// action and the app makes the call.
type (
	switchDeviceMsg struct{ deviceID string }

	volumeMsg struct {
		deviceID string
		percent  int
	}

	muteMsg struct{ deviceID string }

	savePrioritiesMsg struct{ priorities []config.PriorityEntry }

	saveSwitchingMsg struct{ switching config.SwitchingConfig }

	volumeTickMsg struct{ seq int }

	noticeMsg struct {
		kind noticeKind
		text string
	}

	noticeExpiredMsg struct{ at time.Time }

	uptimeTickMsg struct{}
)

// Results of a call to the daemon.
type (
	setDefaultMsg           struct{ err error }
	volumeResultMsg         struct{ err error }
	muteResultMsg           struct{ err error }
	savePrioritiesResultMsg struct{ err error }
	saveSwitchingResultMsg  struct{ err error }
)

func requestDefaultCmd(deviceID string) tea.Cmd {
	return func() tea.Msg { return switchDeviceMsg{deviceID: deviceID} }
}

func requestVolumeCmd(deviceID string, percent int) tea.Cmd {
	return func() tea.Msg { return volumeMsg{deviceID: deviceID, percent: percent} }
}

func requestMuteCmd(deviceID string) tea.Cmd {
	return func() tea.Msg { return muteMsg{deviceID: deviceID} }
}

func savePrioritiesCmd(priorities []config.PriorityEntry) tea.Cmd {
	return func() tea.Msg { return savePrioritiesMsg{priorities: priorities} }
}

func saveSwitchingCmd(switching config.SwitchingConfig) tea.Cmd {
	return func() tea.Msg { return saveSwitchingMsg{switching: switching} }
}

func noticeCmd(kind noticeKind, text string) tea.Cmd {
	return func() tea.Msg { return noticeMsg{kind: kind, text: text} }
}

func volumeTickCmd(seq int) tea.Cmd {
	return tea.Tick(volumeDebounce, func(time.Time) tea.Msg {
		return volumeTickMsg{seq: seq}
	})
}

func uptimeTickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return uptimeTickMsg{} })
}

func noticeExpiryCmd(at time.Time) tea.Cmd {
	return tea.Tick(noticeTTL, func(time.Time) tea.Msg {
		return noticeExpiredMsg{at: at}
	})
}

// subscribeCmd opens the stream the whole UI reads from. The context lives as
// long as the program does: the channel closes on its own when the daemon goes
// away, which is the signal to show it offline and dial again.
func subscribeCmd(client *ipc.Client, gen int) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return subFailedMsg{gen: gen, err: errNoDaemon}
		}
		ch, err := client.Subscribe(context.Background())
		if err != nil {
			return subFailedMsg{gen: gen, err: err}
		}
		return subReadyMsg{gen: gen, ch: ch}
	}
}

// waitSnapshotCmd takes the next snapshot off the stream. It is reissued after
// every one, which is how a channel becomes a sequence of messages.
func waitSnapshotCmd(ch <-chan ipc.Snapshot, gen int) tea.Cmd {
	return func() tea.Msg {
		snap, ok := <-ch
		if !ok {
			return subClosedMsg{gen: gen}
		}
		return snapshotMsg{gen: gen, snap: snap}
	}
}

func retrySubscribeCmd(gen int) tea.Cmd {
	return tea.Tick(subRetryDelay, func(time.Time) tea.Msg {
		return subRetryMsg{gen: gen}
	})
}

func snapshotOnceCmd(client *ipc.Client) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return refreshMsg{err: errNoDaemon}
		}
		snap, err := client.Snapshot()
		return refreshMsg{snap: snap, err: err}
	}
}

func setDefaultCmd(client *ipc.Client, deviceID string) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return setDefaultMsg{err: errNoDaemon}
		}
		return setDefaultMsg{err: client.SetDefault(deviceID)}
	}
}

func setVolumeCmd(client *ipc.Client, deviceID string, percent int) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return volumeResultMsg{err: errNoDaemon}
		}
		return volumeResultMsg{err: client.SetVolume(deviceID, percent)}
	}
}

func toggleMuteCmd(client *ipc.Client, deviceID string) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return muteResultMsg{err: errNoDaemon}
		}
		return muteResultMsg{err: client.ToggleMute(deviceID)}
	}
}

func updatePrioritiesCmd(client *ipc.Client, priorities []config.PriorityEntry) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return savePrioritiesResultMsg{err: errNoDaemon}
		}
		return savePrioritiesResultMsg{err: client.UpdatePriorities(priorities)}
	}
}

func updateSwitchingCmd(client *ipc.Client, switching config.SwitchingConfig) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return saveSwitchingResultMsg{err: errNoDaemon}
		}
		return saveSwitchingResultMsg{err: client.UpdateSwitching(switching.OnConnect, switching.OnDisconnect)}
	}
}
