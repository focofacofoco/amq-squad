//go:build windows

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/omriariav/amq-squad/v2/internal/amqexec"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/procinfo"
	"golang.org/x/sys/windows"
)

func runPlatformAgent(target string, args []string, rec launch.Record, agentDir string, snapshot *launchRecordWriteSnapshot) (bool, error) {
	path, err := exec.LookPath(target)
	if err != nil {
		return true, fmt.Errorf("resolve agent executable %s: %w", target, err)
	}
	cmd := exec.Command(path, args...)
	cmd.Dir = rec.CWD
	cmd.Env = windowsLaunchEnv(os.Environ(), rec)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}

	job, err := newLaunchJob()
	if err != nil {
		return true, err
	}
	defer windows.CloseHandle(job)
	if err := cmd.Start(); err != nil {
		return true, fmt.Errorf("start %s: %w", target, err)
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = cmd.Process.Kill()
		return true, fmt.Errorf("open launched process %d: %w", cmd.Process.Pid, err)
	}
	assignErr := windows.AssignProcessToJobObject(job, process)
	windows.CloseHandle(process)
	if assignErr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return true, fmt.Errorf("assign launched process %d to job: %w", cmd.Process.Pid, assignErr)
	}

	rec.AgentPID = cmd.Process.Pid
	rec.StartedAt = time.Now().UTC()
	if started, ok := procinfo.StartTime(rec.AgentPID); ok {
		rec.StartedAt = started.UTC()
	}
	err = launch.WithRecordLock(agentDir, func() error {
		if err := launch.WriteUnderRecordLock(agentDir, rec); err != nil {
			return err
		}
		written, err := launch.Read(agentDir)
		if err != nil {
			return err
		}
		snapshot.Written = written
		return nil
	})
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return true, fmt.Errorf("update launch record with child pid: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return true, fmt.Errorf("agent exited with code %d", exitErr.ExitCode())
		}
		return true, fmt.Errorf("wait for agent: %w", err)
	}
	return true, nil
}

func windowsLaunchEnv(parent []string, rec launch.Record) []string {
	env := amqexec.NoUpdateCheckEnv(envWithoutAMQIdentity(parent))
	baseRoot := strings.TrimSpace(rec.BaseRoot)
	if baseRoot == "" {
		baseRoot = rec.Root
	}
	env = append(env,
		"AM_ROOT="+rec.Root,
		"AM_BASE_ROOT="+baseRoot,
		"AM_ME="+rec.Handle,
	)
	if !sameResolvedDir(rec.Root, baseRoot) && strings.TrimSpace(rec.Session) != "" {
		env = append(env, "AM_SESSION="+rec.Session)
	}
	return env
}

func newLaunchJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("create process job: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
	if err != nil {
		windows.CloseHandle(job)
		return 0, fmt.Errorf("configure process job: %w", err)
	}
	return job, nil
}
