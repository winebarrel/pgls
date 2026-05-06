package lsp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStartSchemaWatcher_RetriesAfterFailure(t *testing.T) {
	// First call points at a missing directory: addDirsRecursively
	// errors and the watcher isn't installed. The previous
	// sync.Once-based implementation would treat the failed attempt
	// as "done" and reject every subsequent call, even after the
	// directory had been created. Now each call is a retry until
	// one actually launches the goroutine.
	t.Cleanup(resetWatcher)
	resetWatcher()

	missing := filepath.Join(t.TempDir(), "does-not-exist-yet")
	startSchemaWatcher(missing)

	watcherMu.Lock()
	started := watcherStarted
	watcherMu.Unlock()
	require.False(t, started, "first call must not record success when addDirsRecursively fails")

	// Now the directory exists — a retry should install the watcher.
	require.NoError(t, os.MkdirAll(missing, 0o755))
	startSchemaWatcher(missing)

	watcherMu.Lock()
	started = watcherStarted
	watcherMu.Unlock()
	assert.True(t, started, "second call with a real directory must succeed")
}

func TestStartSchemaWatcher_NoOpAfterSuccess(t *testing.T) {
	// Once a watcher is up the function should be a no-op — a second
	// successful call shouldn't spin up a duplicate goroutine.
	t.Cleanup(resetWatcher)
	resetWatcher()

	dir := t.TempDir()
	startSchemaWatcher(dir)
	watcherMu.Lock()
	first := watcherStarted
	watcherMu.Unlock()
	require.True(t, first)

	startSchemaWatcher(dir) // should be a no-op; just exercising the path
	watcherMu.Lock()
	defer watcherMu.Unlock()
	assert.True(t, watcherStarted, "still started after second call")
}

// resetWatcher tears the watcher down to a clean state between
// tests. Closing watcherInstance signals runWatcher's select to exit
// (fsnotify closes the Events/Errors channels on Close), so the
// goroutine and file descriptor don't leak across cases. fsnotify's
// Close is idempotent, so the defer in runWatcher remains harmless.
func resetWatcher() {
	watcherMu.Lock()
	defer watcherMu.Unlock()
	if watcherInstance != nil {
		_ = watcherInstance.Close()
		watcherInstance = nil
	}
	watcherStarted = false
}
