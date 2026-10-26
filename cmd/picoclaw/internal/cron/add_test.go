package cron

import (
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sipeed/picoclaw/pkg/cron"
)

func TestNewAddSubcommand(t *testing.T) {
	fn := func() string { return "" }
	cmd := newAddCommand(fn)

	require.NotNil(t, cmd)

	assert.Equal(t, "add", cmd.Use)
	assert.Equal(t, "Add a new scheduled job", cmd.Short)

	assert.True(t, cmd.HasFlags())

	assert.NotNil(t, cmd.Flags().Lookup("every"))
	assert.NotNil(t, cmd.Flags().Lookup("cron"))
	assert.NotNil(t, cmd.Flags().Lookup("to"))
	assert.NotNil(t, cmd.Flags().Lookup("channel"))
	assert.NotNil(t, cmd.Flags().Lookup("deliver"))

	nameFlag := cmd.Flags().Lookup("name")
	require.NotNil(t, nameFlag)

	messageFlag := cmd.Flags().Lookup("message")
	require.NotNil(t, messageFlag)

	val, found := nameFlag.Annotations[cobra.BashCompOneRequiredFlag]
	require.True(t, found)
	require.NotEmpty(t, val)
	assert.Equal(t, "true", val[0])

	val, found = messageFlag.Annotations[cobra.BashCompOneRequiredFlag]
	require.True(t, found)
	require.NotEmpty(t, val)
	assert.Equal(t, "true", val[0])

	for _, name := range []string{"channel", "to"} {
		flag := cmd.Flags().Lookup(name)
		require.NotNil(t, flag)
		val, found = flag.Annotations[cobra.BashCompOneRequiredFlag]
		require.True(t, found, "%s should be required", name)
		require.NotEmpty(t, val)
		assert.Equal(t, "true", val[0])
	}
}

func TestNewAddCommandEveryAndCronMutuallyExclusive(t *testing.T) {
	cmd := newAddCommand(func() string { return "testing" })

	cmd.SetArgs([]string{
		"--name", "job",
		"--message", "hello",
		"--every", "10",
		"--cron", "0 9 * * *",
		"--channel", "telegram",
		"--to", "chat-1",
	})

	err := cmd.Execute()
	require.Error(t, err)
}

func TestAddCommandRequiresDeliveryTarget(t *testing.T) {
	cmd := newAddCommand(func() string {
		return filepath.Join(t.TempDir(), "jobs.json")
	})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--name", "test", "--message", "hello", "--every", "60"})

	if err := cmd.Execute(); err == nil {
		t.Fatal("cron add should require --channel and --to")
	}
}

func TestAddCommandDeliverFlag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	cmd := newAddCommand(func() string { return path })
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{
		"--name", "test",
		"--message", "hello",
		"--every", "60",
		"--channel", "telegram",
		"--to", "chat-1",
		"--deliver",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("cron add failed: %v", err)
	}

	cs := cron.NewCronService(path, nil)
	jobs := cs.ListJobs(true)
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(jobs))
	}
	if !jobs[0].Payload.Deliver {
		t.Fatalf("deliver not persisted: %+v", jobs[0].Payload)
	}
	if jobs[0].Payload.Channel != "telegram" || jobs[0].Payload.To != "chat-1" {
		t.Fatalf("target = %s/%s, want telegram/chat-1", jobs[0].Payload.Channel, jobs[0].Payload.To)
	}
}
