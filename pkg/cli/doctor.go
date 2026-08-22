package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/systemstart/nix-compose/pkg/doctor"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check whether this machine can run a composition",
	Long: `Check whether this machine can run a composition.

Reports the runtime, cgroup driver, Nix, CNI plugins, and the two environment
problems that fail with messages naming the symptom rather than the cause: a
missing iptables on the runtime's PATH, and container log files a non-root user
cannot read.

Exits non-zero if anything would stop a composition running, so it can gate a
setup script.`,
	RunE:          runDoctor,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func runDoctor(cmd *cobra.Command, args []string) error {
	report := doctor.Run(context.Background(), flagCRISocket)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, check := range report.Checks {
		_, _ = fmt.Fprintf(w, "%s %s\t%s\n", check.Status.Symbol(), check.Name, check.Detail)
		if check.Fix != "" {
			for i, line := range wrapFix(check.Fix, 64) {
				prefix := "  "
				if i == 0 {
					prefix = "→ "
				}
				_, _ = fmt.Fprintf(w, "\t%s%s\n", prefix, line)
			}
		}
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("writing report: %w", err)
	}

	if failed := report.Failed(); failed > 0 {
		fmt.Printf("\n%d check(s) failed.\n", failed)
		// Reported above, in full. Anything printed here would repeat it.
		return errSilent
	}
	return nil
}

// errSilent carries a non-zero exit without printing anything further.
var errSilent = &silentError{}

type silentError struct{}

func (e *silentError) Error() string { return "" }

// wrapFix breaks a fix into terminal-friendly lines without splitting words.
func wrapFix(text string, width int) []string {
	var lines []string
	var line strings.Builder

	for _, word := range strings.Fields(text) {
		if line.Len() > 0 && line.Len()+1+len(word) > width {
			lines = append(lines, line.String())
			line.Reset()
		}
		if line.Len() > 0 {
			line.WriteByte(' ')
		}
		line.WriteString(word)
	}
	if line.Len() > 0 {
		lines = append(lines, line.String())
	}
	return lines
}
