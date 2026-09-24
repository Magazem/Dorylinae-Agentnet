//go:build windows

package device

import (
	"errors"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procNtResumeProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")

// processTree is a started command inside a Job object with
// KILL_ON_JOB_CLOSE: every process it creates joins the job, so killing or
// closing the job ends the whole tree.
type processTree struct {
	job  windows.Handle
	once sync.Once
	mu   sync.Mutex
	done bool
}

// startTree starts cmd suspended, with no console window, puts it in a new
// job before it runs a single instruction (so no child can escape the job),
// then resumes it.
func startTree(cmd *exec.Cmd) (*processTree, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NO_WINDOW | windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP,
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	infoPtr := uintptr(unsafe.Pointer(&info)) //nolint:gosec // fixed struct pointer for the Win32 job-info call
	if _, err := windows.SetInformationJobObject(job, uint32(windows.JobObjectExtendedLimitInformation),
		infoPtr, uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	fail := func(err error) (*processTree, error) {
		// Suspended and outside the job, or inside it: kill it either way.
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = windows.CloseHandle(job)
		return nil, err
	}
	proc, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME, false, uint32(cmd.Process.Pid)) //nolint:gosec // os/exec PIDs are always positive
	if err != nil {
		return fail(err)
	}
	defer func() { _ = windows.CloseHandle(proc) }()
	if err := windows.AssignProcessToJobObject(job, proc); err != nil {
		return fail(err)
	}
	if r, _, _ := procNtResumeProcess.Call(uintptr(proc)); r != 0 {
		return fail(errors.New("device: resume process failed"))
	}
	return &processTree{job: job}, nil
}

// kill terminates every process in the job.
func (t *processTree) kill() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.done {
		_ = windows.TerminateJobObject(t.job, 1)
	}
}

// close releases the job; KILL_ON_JOB_CLOSE ends anything still in it.
func (t *processTree) close() {
	t.once.Do(func() {
		t.mu.Lock()
		t.done = true
		t.mu.Unlock()
		_ = windows.CloseHandle(t.job)
	})
}
