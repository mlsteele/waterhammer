// Run go-test until it fails. Good for debugging flaky tests.

package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

const globalVerbose = false

func main() {
	ctx := context.Background()
	err := main2(ctx)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
	}
}

func main2(ctx context.Context) error {
	log(ctx, true, "start")
	var filter string
	if len(os.Args) >= 3 {
		return fmt.Errorf("too many arguments")
	}
	if len(os.Args) >= 2 {
		filter = os.Args[1]
	}
	if filter == "" {
		log(ctx, false, "filter: all tests")
	} else {
		log(ctx, false, "filter: %q", filter)
	}
	i := 0
	for {
		log(ctx, false, "round %v", i)

		err := round(ctx, filter)
		if err != nil {
			log(ctx, false, "round %v -> Error: %v", i, err)
			return err
		}
		log(ctx, false, "round %v -> OK", i)

		i++
	}
}

func round(ctx context.Context, filter string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return roundInner(ctx, filter)
}

func roundInner(ctx context.Context, filter string) error {
	testLogPath := "/tmp/test.log"
	testLogSwapPath := testLogPath + ".swp"

	testLogSwapFile, err := os.Create(testLogSwapPath)
	if err != nil {
		return fmt.Errorf("error creating test log file: %v", err)
	}

	if filter == "" {
		filter = ".*"
	}
	cmdparts := strings.Split(fmt.Sprintf(`go test -v -run ^%s$`, filter), " ")
	cmd := exec.CommandContext(ctx, cmdparts[0], cmdparts[1:]...)
	// Setpgid copied from builderator. I don't remember what it was for there.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout1, err := cmd.StdoutPipe()
	stdout2 := io.TeeReader(stdout1, testLogSwapFile)
	if err != nil {
		return err
	}
	stdout := bufio.NewScanner(stdout2)

	stderr1, err := cmd.StderrPipe()
	stderr2 := io.TeeReader(stderr1, testLogSwapFile)
	if err != nil {
		return err
	}
	stderr := bufio.NewScanner(stderr2)

	log(ctx, true, "cmd start")
	err = cmd.Start()
	if err != nil {
		return err
	}

	var wg sync.WaitGroup

	driveScanner := func(ctx context.Context, name string, scanner *bufio.Scanner) {
		wg.Add(1)
		log := func(ctx context.Context, verbose bool, format string, args ...interface{}) {
			if !verbose || globalVerbose {
				if name == "stdout" {
					fmt.Printf("%s\n", fmt.Sprintf(format, args...))
				} else {
					log(ctx, false, fmt.Sprintf("(%s) %s", name, fmt.Sprintf(format, args...)))
				}
			}
		}
		go func(ctx context.Context) {
			log(ctx, true, "scanner start")
			var i int
			for scanner.Scan() {
				i++
				line := scanner.Text()
				if strings.Contains(line, "testing: warning: no tests to run") {
					log(ctx, false, "NO TESTS RUN")
				}
				if strings.Contains(line, "=== RUN") {
					log(ctx, false, "%v", line)
				}
				if strings.Contains(line, "--- FAIL") {
					log(ctx, false, "%v", line)
				}
				if strings.Contains(line, "--- PASS") {
					log(ctx, false, "%v", line)
				}
				if strings.Contains(line, "_test.go") {
					log(ctx, false, "%v", line)
				}
				// log(ctx, "scanner line: %v", i)
				// log(ctx, "scanner line: %v", line)
			}
			err := scanner.Err()
			if err != nil {
				log(ctx, false, "scanner error: %v", err)
				return
			}
			log(ctx, true, "scanner complete")
			wg.Done()
		}(ctx)
	}

	driveScanner(ctx, "stdout", stdout)
	driveScanner(ctx, "stderr", stderr)

	log(ctx, true, "wg wait")
	wg.Wait()

	log(ctx, true, "moving log file")
	os.Rename(testLogSwapPath, testLogPath)

	// TODO I think this is flawed.
	// Maybe the process exits early or without closing the scanners and that causes this
	// driver to freeze.

	log(ctx, true, "cmd wait")
	exit := cmd.Wait()
	select {
	case <-ctx.Done():
		log(ctx, false, "cmd done & canceled: %v", exit)
		return ctx.Err()
	default:
	}
	if exit != nil {
		return fmt.Errorf("test failed: %v", exit)
	}
	log(ctx, true, "cmd done")

	return nil
}

func log(ctx context.Context, verbose bool, format string, args ...interface{}) {
	if !verbose || globalVerbose {
		fmt.Printf("[H] %s\n", fmt.Sprintf(format, args...))
	}
}
