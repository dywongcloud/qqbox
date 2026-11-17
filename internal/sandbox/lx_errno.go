//go:build darwin || linux || windows

package sandbox

import "strconv"

// lxErrno is a Linux errno number, the value the guest sees. Host errors are
// converted to it where a host call returns (hostErrno); everything above that
// boundary deals in Linux numbers only.
type lxErrno int64

const (
	lxE2BIG           lxErrno = 7
	lxEACCES          lxErrno = 13
	lxEADDRINUSE      lxErrno = 98
	lxEADDRNOTAVAIL   lxErrno = 99
	lxEAFNOSUPPORT    lxErrno = 97
	lxEAGAIN          lxErrno = 11
	lxEALREADY        lxErrno = 114
	lxEBADF           lxErrno = 9
	lxEBADMSG         lxErrno = 74
	lxEBUSY           lxErrno = 16
	lxECANCELED       lxErrno = 125
	lxECHILD          lxErrno = 10
	lxECHRNG          lxErrno = 44
	lxECONNABORTED    lxErrno = 103
	lxECONNREFUSED    lxErrno = 111
	lxECONNRESET      lxErrno = 104
	lxEDESTADDRREQ    lxErrno = 89
	lxEDOM            lxErrno = 33
	lxEDQUOT          lxErrno = 122
	lxEEXIST          lxErrno = 17
	lxEFAULT          lxErrno = 14
	lxEFBIG           lxErrno = 27
	lxEHOSTDOWN       lxErrno = 112
	lxEHOSTUNREACH    lxErrno = 113
	lxEIDRM           lxErrno = 43
	lxEILSEQ          lxErrno = 84
	lxEINPROGRESS     lxErrno = 115
	lxEINTR           lxErrno = 4
	lxEINVAL          lxErrno = 22
	lxEIO             lxErrno = 5
	lxEISCONN         lxErrno = 106
	lxEISDIR          lxErrno = 21
	lxELOOP           lxErrno = 40
	lxEMFILE          lxErrno = 24
	lxEMLINK          lxErrno = 31
	lxEMSGSIZE        lxErrno = 90
	lxEMULTIHOP       lxErrno = 72
	lxENAMETOOLONG    lxErrno = 36
	lxENETDOWN        lxErrno = 100
	lxENETRESET       lxErrno = 102
	lxENETUNREACH     lxErrno = 101
	lxENFILE          lxErrno = 23
	lxENOBUFS         lxErrno = 105
	lxENODATA         lxErrno = 61
	lxENODEV          lxErrno = 19
	lxENOENT          lxErrno = 2
	lxENOEXEC         lxErrno = 8
	lxENOLCK          lxErrno = 37
	lxENOLINK         lxErrno = 67
	lxENOMEM          lxErrno = 12
	lxENOMSG          lxErrno = 42
	lxENOPROTOOPT     lxErrno = 92
	lxENOSPC          lxErrno = 28
	lxENOSR           lxErrno = 63
	lxENOSTR          lxErrno = 60
	lxENOSYS          lxErrno = 38
	lxENOTBLK         lxErrno = 15
	lxENOTCONN        lxErrno = 107
	lxENOTDIR         lxErrno = 20
	lxENOTEMPTY       lxErrno = 39
	lxENOTRECOVERABLE lxErrno = 131
	lxENOTSOCK        lxErrno = 88
	lxENOTSUP         lxErrno = 95
	lxENOTTY          lxErrno = 25
	lxENXIO           lxErrno = 6
	lxEOPNOTSUPP      lxErrno = 95
	lxEOVERFLOW       lxErrno = 75
	lxEOWNERDEAD      lxErrno = 130
	lxEPERM           lxErrno = 1
	lxEPFNOSUPPORT    lxErrno = 96
	lxEPIPE           lxErrno = 32
	lxEPROTO          lxErrno = 71
	lxEPROTONOSUPPORT lxErrno = 93
	lxEPROTOTYPE      lxErrno = 91
	lxERANGE          lxErrno = 34
	lxEROFS           lxErrno = 30
	lxESHUTDOWN       lxErrno = 108
	lxESOCKTNOSUPPORT lxErrno = 94
	lxESPIPE          lxErrno = 29
	lxESRCH           lxErrno = 3
	lxESTALE          lxErrno = 116
	lxETIME           lxErrno = 62
	lxETIMEDOUT       lxErrno = 110
	lxETOOMANYREFS    lxErrno = 109
	lxETXTBSY         lxErrno = 26
	lxEUSERS          lxErrno = 87
	lxEWOULDBLOCK     lxErrno = 11
	lxEXDEV           lxErrno = 18

	// errRestart and errRestartNoHand are Linux's ERESTARTSYS and
	// ERESTARTNOHAND: a wait a signal cut short, which the stub restarts or
	// fails with EINTR.
	errRestart       lxErrno = 512
	errRestartNoHand lxErrno = 514
	errRestartBlock  lxErrno = 516 // ERESTART_RESTARTBLOCK: resumed by restart_syscall (g.restart)
)

// lxErr carries a Linux errno through a path that returns an error; osErrno
// recovers it.
type lxErr lxErrno

func (e lxErr) Error() string { return "linux errno " + strconv.Itoa(int(e)) }

// sErr is a syscall's return value for errno e.
func sErr(e lxErrno) int64 { return -int64(e) }
