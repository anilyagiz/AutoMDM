package client

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewOrbitClientBypassEndUserAuth(t *testing.T) {
	newClient := func(bypassEndUserAuth bool) *OrbitClient {
		oc, err := NewOrbitClient(
			t.TempDir(),
			"https://automdm.example.com",
			"",
			true,
			"secret",
			nil,
			automdm.OrbitHostInfo{HardwareUUID: "uuid", Hostname: "host"},
			nil,
			nil,
			"",
			bypassEndUserAuth,
		)
		require.NoError(t, err)
		return oc
	}

	// With bypass enabled, orbit must not advertise the end-user auth capability.
	require.NotContains(t, newClient(true).ClientCapabilities, automdm.CapabilityEndUserAuth)

	// Without bypass, capabilities are unchanged from the default set.
	require.Equal(t, automdm.GetOrbitClientCapabilities(), newClient(false).ClientCapabilities)
}

func clientWithConfig(t *testing.T, cfg *automdm.OrbitConfig) *OrbitClient {
	srv := &configServer{}
	srv.set(*cfg)
	return newNudgeTestClient(t, srv)
}

func TestOrbitClientResponseHeaderTimeout(t *testing.T) {
	orig := responseHeaderTimeout
	responseHeaderTimeout = 100 * time.Millisecond
	t.Cleanup(func() { responseHeaderTimeout = orig })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte("installer"))
	}))
	t.Cleanup(srv.Close)

	oc, err := NewOrbitClient(t.TempDir(), srv.URL, "", true, "secret", nil,
		automdm.OrbitHostInfo{HardwareUUID: "uuid", Hostname: "host"}, nil, nil, "", false)
	require.NoError(t, err)
	_, err = oc.DownloadSoftwareInstallerFromURL(srv.URL+"/app.pkg", "app.pkg", t.TempDir(), nil)
	require.ErrorContains(t, err, "timeout awaiting response headers")
}

func TestConfigReceiverCalls(t *testing.T) {
	var called1, called2 bool

	testmsg := json.RawMessage(`"testing"`)

	rfunc1 := automdm.OrbitConfigReceiverFunc(func(cfg *automdm.OrbitConfig) error {
		if !reflect.DeepEqual(cfg.Flags, testmsg) {
			return errors.New("not equal testmsg")
		}
		called1 = true
		return nil
	})
	rfunc2 := automdm.OrbitConfigReceiverFunc(func(cfg *automdm.OrbitConfig) error {
		if !reflect.DeepEqual(cfg.Flags, testmsg) {
			return errors.New("not equal testmsg")
		}
		called2 = true
		return nil
	})

	client := clientWithConfig(t, &automdm.OrbitConfig{Flags: testmsg})
	client.RegisterConfigReceiver(rfunc1)
	client.RegisterConfigReceiver(rfunc2)

	err := client.RunConfigReceivers()
	require.NoError(t, err)

	require.True(t, called1)
	require.True(t, called2)
}

func TestConfigReceiverErrors(t *testing.T) {
	var called1, called2 bool

	rfunc1 := automdm.OrbitConfigReceiverFunc(func(cfg *automdm.OrbitConfig) error {
		called1 = true
		return nil
	})
	rfunc2 := automdm.OrbitConfigReceiverFunc(func(cfg *automdm.OrbitConfig) error {
		called2 = true
		return nil
	})
	err1 := errors.New("error1")
	err2 := errors.New("error2")
	efunc1 := automdm.OrbitConfigReceiverFunc(func(cfg *automdm.OrbitConfig) error {
		return err1
	})
	efunc2 := automdm.OrbitConfigReceiverFunc(func(cfg *automdm.OrbitConfig) error {
		return err2
	})
	// Make sure we don't get stuck or crash on receiver panic
	pfunc := automdm.OrbitConfigReceiverFunc(func(cfg *automdm.OrbitConfig) error {
		panic("woah")
	})

	client := clientWithConfig(t, &automdm.OrbitConfig{})
	client.RegisterConfigReceiver(efunc1)
	client.RegisterConfigReceiver(rfunc1)
	client.RegisterConfigReceiver(efunc2)
	client.RegisterConfigReceiver(rfunc2)
	client.RegisterConfigReceiver(pfunc)

	err := client.RunConfigReceivers()
	require.ErrorIs(t, err, err1)
	require.ErrorIs(t, err, err2)

	require.True(t, called1)
	require.True(t, called2)
}

func TestExecuteConfigReceiversCancel(t *testing.T) {
	client := clientWithConfig(t, &automdm.OrbitConfig{})
	client.DefaultConfigPollInterval = 100 * time.Millisecond

	var calls1, calls2 int
	requiredCalls := 4

	cfunc := automdm.OrbitConfigReceiverFunc(func(cfg *automdm.OrbitConfig) error {
		calls1++
		if calls1 == requiredCalls {
			client.receiverUpdateCancelFunc()
		}
		return nil
	})

	rfunc := automdm.OrbitConfigReceiverFunc(func(cfg *automdm.OrbitConfig) error {
		calls2++
		return nil
	})

	client.RegisterConfigReceiver(cfunc)
	client.RegisterConfigReceiver(rfunc)

	err := client.ExecuteConfigReceivers()

	require.Nil(t, err)
	require.Equal(t, requiredCalls, calls1)
	require.Equal(t, requiredCalls, calls2)
}

func TestExecuteConfigReceiversInterrupt(t *testing.T) {
	client := clientWithConfig(t, &automdm.OrbitConfig{})
	defer client.receiverUpdateCancelFunc()

	client.DefaultConfigPollInterval = 100 * time.Millisecond

	var called bool
	rfunc := automdm.OrbitConfigReceiverFunc(func(cfg *automdm.OrbitConfig) error {
		called = true
		return nil
	})

	client.RegisterConfigReceiver(rfunc)

	finChan := make(chan error)
	go func() {
		finChan <- client.ExecuteConfigReceivers()
	}()

	go func() {
		time.Sleep(500 * time.Millisecond)
		client.receiverUpdateCancelFunc()
	}()

	select {
	case err := <-finChan:
		require.Nil(t, err)
		require.True(t, called)
	case <-time.NewTimer(2 * time.Second).C:
		require.Fail(t, "receiver interrupt cancel didn't work")
	}
}

func TestExecuteConfigReceiversNoBackoffOnError(t *testing.T) {
	client := clientWithConfig(t, &automdm.OrbitConfig{})
	client.DefaultConfigPollInterval = 500 * time.Millisecond

	var callTimes []time.Time
	targetCalls := 4

	rfunc := automdm.OrbitConfigReceiverFunc(func(cfg *automdm.OrbitConfig) error {
		callTimes = append(callTimes, time.Now())
		if len(callTimes) >= targetCalls {
			client.receiverUpdateCancelFunc()
			return nil
		}
		return errors.New("receiver failed")
	})

	client.RegisterConfigReceiver(rfunc)

	done := make(chan error, 1)
	go func() { done <- client.ExecuteConfigReceivers() }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("test timed out waiting for ExecuteConfigReceivers")
	}
	require.Len(t, callTimes, targetCalls)

	// Errors don't grow the interval: every run is a default interval after the previous one.
	for i := 1; i < len(callTimes); i++ {
		assert.Less(t, callTimes[i].Sub(callTimes[i-1]), 900*time.Millisecond, "interval %d->%d", i-1, i)
	}
}
