//go:build windows

package sandbox

import (
	"errors"

	"golang.org/x/sys/windows"
)

// winToLinuxErrno maps the Win32 errors host calls return to Linux errnos.
// Anything else is EIO.
var winToLinuxErrno = map[windows.Errno]lxErrno{
	windows.ERROR_FILE_NOT_FOUND:        lxENOENT,
	windows.ERROR_PATH_NOT_FOUND:        lxENOENT,
	windows.ERROR_INVALID_NAME:          lxENOENT,
	windows.ERROR_BAD_PATHNAME:          lxENOENT,
	windows.ERROR_INVALID_DRIVE:         lxENOENT,
	windows.ERROR_ACCESS_DENIED:         lxEACCES,
	windows.ERROR_INVALID_HANDLE:        lxEBADF,
	windows.ERROR_NOT_ENOUGH_MEMORY:     lxENOMEM,
	windows.ERROR_OUTOFMEMORY:           lxENOMEM,
	windows.ERROR_NOT_SAME_DEVICE:       lxEXDEV,
	windows.ERROR_WRITE_PROTECT:         lxEROFS,
	windows.ERROR_SHARING_VIOLATION:     lxEBUSY,
	windows.ERROR_LOCK_VIOLATION:        lxEAGAIN,
	windows.ERROR_HANDLE_DISK_FULL:      lxENOSPC,
	windows.ERROR_DISK_FULL:             lxENOSPC,
	windows.ERROR_NOT_SUPPORTED:         lxEOPNOTSUPP,
	windows.ERROR_FILE_EXISTS:           lxEEXIST,
	windows.ERROR_ALREADY_EXISTS:        lxEEXIST,
	windows.ERROR_INVALID_PARAMETER:     lxEINVAL,
	windows.ERROR_NEGATIVE_SEEK:         lxEINVAL,
	windows.ERROR_BROKEN_PIPE:           lxEPIPE,
	windows.ERROR_NO_DATA:               lxEPIPE,
	windows.ERROR_DIR_NOT_EMPTY:         lxENOTEMPTY,
	windows.ERROR_FILENAME_EXCED_RANGE:  lxENAMETOOLONG,
	windows.ERROR_DIRECTORY:             lxENOTDIR,
	windows.ERROR_CANT_RESOLVE_FILENAME: lxELOOP,
	windows.ERROR_PRIVILEGE_NOT_HELD:    lxEPERM,
	windows.ERROR_OPERATION_ABORTED:     lxEINTR,
	windows.ERROR_IO_PENDING:            lxEAGAIN,
	windows.ERROR_NOT_A_REPARSE_POINT:   lxEINVAL,
	windows.ERROR_TOO_MANY_LINKS:        lxEMLINK,
	windows.ERROR_TOO_MANY_OPEN_FILES:   lxEMFILE,

	// Winsock
	10004: lxEINTR,
	10009: lxEBADF,
	10013: lxEACCES,
	10014: lxEFAULT,
	10022: lxEINVAL,
	10024: lxEMFILE,
	10035: lxEAGAIN,
	10036: lxEINPROGRESS,
	10037: lxEALREADY,
	10038: lxENOTSOCK,
	10039: lxEDESTADDRREQ,
	10040: lxEMSGSIZE,
	10041: lxEPROTOTYPE,
	10042: lxENOPROTOOPT,
	10043: lxEPROTONOSUPPORT,
	10044: lxESOCKTNOSUPPORT,
	10045: lxEOPNOTSUPP,
	10046: lxEPFNOSUPPORT,
	10047: lxEAFNOSUPPORT,
	10048: lxEADDRINUSE,
	10049: lxEADDRNOTAVAIL,
	10050: lxENETDOWN,
	10051: lxENETUNREACH,
	10052: lxENETRESET,
	10053: lxECONNABORTED,
	10054: lxECONNRESET,
	10055: lxENOBUFS,
	10056: lxEISCONN,
	10057: lxENOTCONN,
	10058: lxESHUTDOWN,
	10060: lxETIMEDOUT,
	10061: lxECONNREFUSED,
	10064: lxEHOSTDOWN,
	10065: lxEHOSTUNREACH,
	1225:  lxECONNREFUSED, // ERROR_CONNECTION_REFUSED
	1236:  lxECONNABORTED, // ERROR_CONNECTION_ABORTED
	64:    lxECONNRESET,   // ERROR_NETNAME_DELETED
	1231:  lxENETUNREACH,  // ERROR_NETWORK_UNREACHABLE
	1232:  lxEHOSTUNREACH, // ERROR_HOST_UNREACHABLE
}

func winErrno(e windows.Errno) lxErrno {
	if l, ok := winToLinuxErrno[e]; ok {
		return l
	}
	return lxEIO
}

func asErrno(err error, e *windows.Errno) bool { return errors.As(err, e) }
