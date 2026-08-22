package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"syscall"

	"github.com/spf13/cobra"
	orchestratev1 "github.com/systemstart/nix-compose/api/orchestrate/v1"
	"github.com/systemstart/nix-compose/pkg/eval"
	"github.com/systemstart/nix-compose/pkg/logs"
	"github.com/systemstart/nix-compose/pkg/orchestrate/client"
)

var (
	logsFollow      bool
	logsTail        string
	logsTimestamps  bool
	logsSince       string
	logsNoLogPrefix bool
)

var logsCmd = &cobra.Command{
	Use:   "logs [service...]",
	Short: "View service logs",
	RunE:  runLogs,
}

func init() {
	logsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", false, "follow log output")
	logsCmd.Flags().StringVar(&logsTail, "tail", "", "number of lines to show from the end (e.g. 100, all)")
	logsCmd.Flags().BoolVarP(&logsTimestamps, "timestamps", "t", false, "show timestamps")
	logsCmd.Flags().StringVar(&logsSince, "since", "", "show logs since timestamp (e.g. 2021-01-01T00:00:00Z, 42m)")
	logsCmd.Flags().BoolVar(&logsNoLogPrefix, "no-log-prefix", false, "don't print service name prefix")
}

func runLogs(_ *cobra.Command, args []string) error {
	ctx := context.Background()

	// Try remote path.
	if rc := dialRemote(ctx); rc != nil {
		defer func() { _ = rc.Close() }()
		return remoteLogs(ctx, rc, args)
	}

	criClient, err := requireCRI(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = criClient.Close() }()
	return doLogsCRI(ctx, args)
}

// remoteLogs streams logs from a remote orchestrate server.
func remoteLogs(ctx context.Context, rc *client.Client, args []string) error {
	dir := projectDir()
	project := projectNameFor(dir, flagProjectName)

	opts := client.LogsOpts{
		Follow:     logsFollow,
		Timestamps: logsTimestamps,
		Tail:       logsTail,
		Since:      logsSince,
	}

	streamCtx := ctx
	if opts.Follow {
		var stop context.CancelFunc
		streamCtx, stop = signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
		defer stop()
	}

	stream, err := rc.Logs(streamCtx, project, args, opts)
	if err != nil {
		return fmt.Errorf("remote logs: %w", err)
	}

	printer := newLogPrinter(logsNoLogPrefix, logsTimestamps)
	for {
		entry, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("receiving log entry: %w", err)
		}
		printer.print(entry)
	}
}

// logPrinter formats and prints remote log entries with colored service prefixes.
type logPrinter struct {
	noPrefix   bool
	timestamps bool
	colors     []string
	reset      string
	colorMap   map[string]string
	colorIdx   int
}

func newLogPrinter(noPrefix, timestamps bool) *logPrinter {
	return &logPrinter{
		noPrefix:   noPrefix,
		timestamps: timestamps,
		colors:     []string{"\033[36m", "\033[33m", "\033[32m", "\033[35m", "\033[34m", "\033[31m"},
		reset:      "\033[0m",
		colorMap:   make(map[string]string),
	}
}

func (p *logPrinter) print(entry *orchestratev1.LogEntry) {
	prefix := ""
	if !p.noPrefix {
		color, ok := p.colorMap[entry.Service]
		if !ok {
			color = p.colors[p.colorIdx%len(p.colors)]
			p.colorMap[entry.Service] = color
			p.colorIdx++
		}
		prefix = fmt.Sprintf("%s%-15s |%s ", color, entry.Service, p.reset)
	}

	ts := ""
	if p.timestamps && entry.Timestamp != nil {
		ts = entry.Timestamp.AsTime().Format("2006-01-02T15:04:05.000000000Z07:00") + " "
	}

	_, _ = fmt.Fprintf(os.Stdout, "%s%s%s\n", prefix, ts, entry.Message)
}

// doLogsCRI reads CRI log files directly.
func doLogsCRI(ctx context.Context, args []string) error {
	dir := projectDir()
	project := projectNameFor(dir, flagProjectName)

	services, err := resolveLogServices(ctx, dir, args)
	if err != nil {
		return err
	}

	opts := logs.Options{
		Follow:      logsFollow,
		Timestamps:  logsTimestamps,
		NoLogPrefix: logsNoLogPrefix,
		Tail:        logsTail,
		Since:       logsSince,
		Warn:        warnUnreadableLog,
	}

	if opts.Follow {
		ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		if err := logs.Follow(ctx, os.Stdout, logs.DefaultLogBase, project, services, opts); err != nil {
			return fmt.Errorf("follow logs: %w", err)
		}
		return nil
	}
	if err := logs.Dump(os.Stdout, logs.DefaultLogBase, project, services, opts); err != nil {
		return fmt.Errorf("dump logs: %w", err)
	}
	return nil
}

// warnUnreadableLog reports a log that exists but could not be read, and names
// the fix. Stderr, so redirecting logs to a file still captures only logs.
//
// The overwhelmingly common cause is containerd creating log files
// root:root 0640 while nix-compose runs as an ordinary user. `nix-compose
// doctor` checks for this, but a warning at the point of failure beats a
// preflight the user ran an hour ago.
func warnUnreadableLog(msg string) {
	fmt.Fprintf(os.Stderr, "Warning: %s\n", msg)
	// Deliberately does not suggest `crictl logs`: crictl opens the same file
	// from the same user and fails identically. Only reading it as root, or
	// having the service write somewhere else, actually works.
	fmt.Fprintf(os.Stderr, "  → container logs are written by the runtime, often root-owned and mode 0640.\n"+
		"    `nix-compose ps -a` still shows each container's state and exit code.\n"+
		"    To read the log itself you need root; `nix-compose doctor` checks for this.\n")
}

// resolveLogServices returns the list of services to show logs for.
// If args are given, they are used directly. Otherwise, all services are
// resolved by evaluating the Nix composition.
func resolveLogServices(ctx context.Context, dir string, args []string) ([]string, error) {
	if len(args) > 0 {
		sorted := make([]string, len(args))
		copy(sorted, args)
		sort.Strings(sorted)
		return sorted, nil
	}

	runner := &eval.ExecRunner{Dir: dir}
	evaluator := &eval.Evaluator{
		Runner:     runner,
		ProjectDir: dir,
		FlakeAttr:  flagFlakeAttr,
		Impure:     flagImpure,
	}

	comp, _, err := evaluator.Eval(ctx)
	if err != nil {
		return nil, fmt.Errorf("evaluation failed: %w", err)
	}

	var services []string
	for name := range comp.Services {
		services = append(services, name)
	}
	sort.Strings(services)
	return services, nil
}
