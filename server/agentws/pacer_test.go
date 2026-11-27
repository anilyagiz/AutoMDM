package agentws

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnCoalescesPendingByType(t *testing.T) {
	c := &conn{
		pending:        make(map[string]string),
		wake:           make(chan struct{}, 1),
		notifiedByType: make(map[string]int64),
	}

	// More notifications than the old drop-oldest buffer held: none is lost,
	// and each type is delivered once with its latest reason.
	for range 20 {
		c.enqueue(automdm.AgentWSMessage{Type: automdm.AgentWSMessageTypeDistributedRead, Reason: automdm.AgentWSReasonLabel})
	}
	c.enqueue(automdm.AgentWSMessage{Type: automdm.AgentWSMessageTypeOrbitConfig, Reason: automdm.AgentWSReasonActivity})
	c.enqueue(automdm.AgentWSMessage{Type: automdm.AgentWSMessageTypeDistributedRead, Reason: automdm.AgentWSReasonDetail})

	assert.Equal(t, []automdm.AgentWSMessage{
		{Type: automdm.AgentWSMessageTypeDistributedRead, Reason: automdm.AgentWSReasonDetail},
		{Type: automdm.AgentWSMessageTypeOrbitConfig, Reason: automdm.AgentWSReasonActivity},
	}, c.takePending())
	assert.Empty(t, c.takePending())

	assert.Equal(t, int64(22), c.notified.Load())
	assert.Equal(t, int64(20), c.coalesced.Load())
	assert.Equal(t, map[string]int64{
		automdm.AgentWSMessageTypeDistributedRead: 21,
		automdm.AgentWSMessageTypeOrbitConfig:     1,
	}, c.notifiedByTypeSnapshot())
	assert.Len(t, c.wake, 1)
}

func TestPacerSpreadsAndMerges(t *testing.T) {
	p := newPacer(NewHub(discardLogger(), time.Minute, 30*time.Second), time.Second)

	ids := make([]uint, 100)
	for i := range ids {
		ids[i] = uint(i + 1)
	}
	require.Equal(t, 100, p.add(automdm.AgentWSMessageTypeOrbitConfig, automdm.AgentWSReasonSettings, ids))
	// Already queued hosts are merged, keeping the latest reason.
	require.Equal(t, 0, p.add(automdm.AgentWSMessageTypeOrbitConfig, automdm.AgentWSReasonMDM, ids[:10]))
	require.Equal(t, 100, p.queueLen())

	// A tick releases its share of the remaining window, FIFO.
	start := p.deadline.Add(-time.Second)
	batch := p.next(start)
	require.Len(t, batch, 10)
	for _, id := range ids[:10] {
		assert.Equal(t, automdm.AgentWSReasonMDM, batch[pacedKey{hostID: id, msgType: automdm.AgentWSMessageTypeOrbitConfig}])
	}
	// Halfway through the window, 90 targets left over 500ms: 18 per tick.
	require.Len(t, p.next(start.Add(500*time.Millisecond)), 18)
	// Once the deadline is reached, everything left is released.
	require.Len(t, p.next(start.Add(2*time.Second)), 72)
	require.Zero(t, p.queueLen())

	// A released host can be queued again.
	require.Equal(t, 1, p.add(automdm.AgentWSMessageTypeOrbitConfig, automdm.AgentWSReasonSettings, ids[:1]))
}

func TestPacerWithoutSpreadReleasesAtOnce(t *testing.T) {
	p := newPacer(NewHub(discardLogger(), time.Minute, 30*time.Second), 0)
	p.add(automdm.AgentWSMessageTypeOrbitConfig, automdm.AgentWSReasonSettings, []uint{1, 2, 3})
	require.Len(t, p.next(time.Now()), 3)
}

type fakeHostLister struct {
	teams map[uint]*uint // host ID → team ID
	err   error
	calls [][]uint
}

func (f *fakeHostLister) ListHostsLiteByIDs(ctx context.Context, ids []uint) ([]*automdm.Host, error) {
	f.calls = append(f.calls, ids)
	if f.err != nil {
		return nil, f.err
	}
	var hosts []*automdm.Host
	for _, id := range ids {
		if teamID, ok := f.teams[id]; ok {
			hosts = append(hosts, &automdm.Host{ID: id, TeamID: teamID})
		}
	}
	return hosts, nil
}

func readMsg(t *testing.T, ws *websocket.Conn, within time.Duration) (automdm.AgentWSMessage, error) {
	t.Helper()
	require.NoError(t, ws.SetReadDeadline(time.Now().Add(within)))
	var msg automdm.AgentWSMessage
	err := ws.ReadJSON(&msg)
	return msg, err
}

func TestHubNotifyScope(t *testing.T) {
	hub := NewHub(discardLogger(), time.Minute, 30*time.Second)
	srv := newTestServer(t, hub)

	_, err := hub.NotifyScope(t.Context(), automdm.AgentWSMessageTypeOrbitConfig, automdm.AgentWSReasonSettings, automdm.AgentNotificationScopeGlobal)
	require.Error(t, err, "scoped notifications not enabled")

	ws1 := dial(t, srv, "key-1")
	ws2 := dial(t, srv, "key-2")
	waitForConnCount(t, hub, 2)

	// Host 1 is in team 7, host 2 in no team.
	lister := &fakeHostLister{teams: map[uint]*uint{1: new(uint(7)), 2: nil}}
	hub.EnableScopedNotifications(t.Context(), lister, 1, 200*time.Millisecond)

	n, err := hub.NotifyScope(t.Context(), automdm.AgentWSMessageTypeOrbitConfig, automdm.AgentWSReasonSettings, automdm.AgentNotificationScopeTeam(7))
	require.NoError(t, err)
	require.Equal(t, 1, n)
	// Held hosts are loaded in batches of 1.
	require.Len(t, lister.calls, 2)

	msg, err := readMsg(t, ws1, 2*time.Second)
	require.NoError(t, err)
	assert.Equal(t, automdm.AgentWSMessage{Type: automdm.AgentWSMessageTypeOrbitConfig, Reason: automdm.AgentWSReasonSettings}, msg)
	// Host 2 is not in team 7. (A read timeout would break ws2, so check
	// the hub's counters instead.)
	time.Sleep(300 * time.Millisecond)
	notified := make(map[uint]int64)
	for _, info := range hub.Snapshot() {
		notified[info.HostID] = info.NotifiedCount
	}
	assert.Equal(t, map[uint]int64{1: 1, 2: 0}, notified)

	// AutoMDM 0 is "No team".
	n, err = hub.NotifyScope(t.Context(), automdm.AgentWSMessageTypeOrbitConfig, automdm.AgentWSReasonSettings, automdm.AgentNotificationScopeTeam(0))
	require.NoError(t, err)
	require.Equal(t, 1, n)
	msg, err = readMsg(t, ws2, 2*time.Second)
	require.NoError(t, err)
	assert.Equal(t, automdm.AgentWSMessage{Type: automdm.AgentWSMessageTypeOrbitConfig, Reason: automdm.AgentWSReasonSettings}, msg)

	n, err = hub.NotifyScope(t.Context(), automdm.AgentWSMessageTypeOrbitConfig, automdm.AgentWSReasonMDM, automdm.AgentNotificationScopeGlobal)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	for _, ws := range []*websocket.Conn{ws1, ws2} {
		msg, err := readMsg(t, ws, 2*time.Second)
		require.NoError(t, err)
		assert.Equal(t, automdm.AgentWSReasonMDM, msg.Reason)
	}
	require.Zero(t, hub.PacerQueueLen())
	assert.Equal(t, map[string]int64{automdm.AgentWSReasonSettings: 2, automdm.AgentWSReasonMDM: 2},
		hub.NotifyCounts()[automdm.AgentWSMessageTypeOrbitConfig])

	lister.err = errors.New("db down")
	_, err = hub.NotifyScope(t.Context(), automdm.AgentWSMessageTypeOrbitConfig, automdm.AgentWSReasonSettings, automdm.AgentNotificationScopeTeam(7))
	require.Error(t, err)

	_, err = hub.NotifyScope(t.Context(), automdm.AgentWSMessageTypeOrbitConfig, automdm.AgentWSReasonSettings, "bogus")
	require.Error(t, err)
}

func TestAgentNotificationScopeTeamID(t *testing.T) {
	id, ok := automdm.AgentNotificationScopeTeam(42).TeamID()
	require.True(t, ok)
	require.Equal(t, uint(42), id)

	for _, s := range []automdm.AgentNotificationScope{automdm.AgentNotificationScopeGlobal, "team:", "team:x", "team:-1", ""} {
		_, ok := s.TeamID()
		require.False(t, ok, s)
	}
}

func TestHubScopeWorkerLoadsHostsOnce(t *testing.T) {
	hub := NewHub(discardLogger(), time.Minute, 30*time.Second)
	srv := newTestServer(t, hub)
	ws1 := dial(t, srv, "key-1")
	ws2 := dial(t, srv, "key-2")
	waitForConnCount(t, hub, 2)

	lister := &fakeHostLister{teams: map[uint]*uint{1: new(uint(7)), 2: new(uint(8))}}
	hub.EnableScopedNotifications(t.Context(), lister, 10, 0)

	// AutoMDM scopes resolved together load the held hosts once.
	n, err := hub.notifyScopes(t.Context(), []ScopedNotification{
		{MsgType: automdm.AgentWSMessageTypeOrbitConfig, Reason: automdm.AgentWSReasonSettings, Scope: automdm.AgentNotificationScopeTeam(7)},
		{MsgType: automdm.AgentWSMessageTypeOrbitConfig, Reason: automdm.AgentWSReasonSettings, Scope: automdm.AgentNotificationScopeTeam(8)},
	})
	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.Len(t, lister.calls, 1)
	for _, ws := range []*websocket.Conn{ws1, ws2} {
		msg, err := readMsg(t, ws, 2*time.Second)
		require.NoError(t, err)
		assert.Equal(t, automdm.AgentWSMessageTypeOrbitConfig, msg.Type)
	}

	// QueueScope goes through the worker.
	hub.QueueScope(ScopedNotification{MsgType: automdm.AgentWSMessageTypeOrbitConfig, Reason: automdm.AgentWSReasonMDM, Scope: automdm.AgentNotificationScopeGlobal})
	for _, ws := range []*websocket.Conn{ws1, ws2} {
		msg, err := readMsg(t, ws, 2*time.Second)
		require.NoError(t, err)
		assert.Equal(t, automdm.AgentWSReasonMDM, msg.Reason)
	}

	// NotifyPaced only queues held hosts.
	hub.NotifyPaced(automdm.AgentWSMessageTypeOrbitConfig, automdm.AgentWSReasonActivity, []uint{2, 99})
	msg, err := readMsg(t, ws2, 2*time.Second)
	require.NoError(t, err)
	assert.Equal(t, automdm.AgentWSReasonActivity, msg.Reason)
}
