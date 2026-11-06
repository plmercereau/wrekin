package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/plmercereau/wrekin/internal/paths"
)

func newLogsCommand(a *app) *cobra.Command {
	var follow bool
	var lines int
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Show the agent log",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := paths.LogFile()
			f, err := os.Open(path)
			if errors.Is(err, os.ErrNotExist) {
				fmt.Fprintf(a.stdout, "No log yet at %s.\n", path)
				return nil
			}
			if err != nil {
				return err
			}
			defer f.Close()
			offset, err := printTail(a.stdout, f, lines)
			if err != nil {
				return err
			}
			if !follow {
				return nil
			}
			for {
				select {
				case <-cmd.Context().Done():
					return nil
				case <-time.After(500 * time.Millisecond):
				}
				info, err := os.Stat(path)
				if err != nil {
					continue
				}
				if info.Size() < offset { // rotated
					offset = 0
				}
				if info.Size() == offset {
					continue
				}
				if _, err := f.Seek(offset, io.SeekStart); err != nil {
					return err
				}
				n, err := io.Copy(a.stdout, f)
				if err != nil {
					return err
				}
				offset += n
			}
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep printing new lines")
	cmd.Flags().IntVarP(&lines, "lines", "n", 100, "number of lines to show")
	return cmd
}

// printTail prints the last n lines and returns the file size read.
func printTail(w io.Writer, f *os.File, n int) (int64, error) {
	var all []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		all = append(all, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	if n > 0 && len(all) > n {
		all = all[len(all)-n:]
	}
	if len(all) > 0 {
		fmt.Fprintln(w, strings.Join(all, "\n"))
	}
	return f.Seek(0, io.SeekCurrent)
}
