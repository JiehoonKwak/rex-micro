package hid

import (
	"bytes"
	"testing"
)

func TestOutputReportForCreatorMicro2(t *testing.T) {
	usb := []byte{2, 3, 'a', 'b', 'c'}
	got := outputReport(CreatorMicro2ProductID, usb)
	if want := append([]byte{ReportID}, usb...); !bytes.Equal(got, want) {
		t.Fatalf("Creator Micro 2 USB report = %v, want %v", got, want)
	}
	if got := outputReport(CreatorMicro2ProductID, []byte{ReportID, 2, 1, 'a'}); !bytes.Equal(got, []byte{ReportID, 2, 1, 'a'}) {
		t.Fatalf("Creator Micro 2 BLE report changed: %v", got)
	}
}

func TestOutputReportForCodexMicro(t *testing.T) {
	report := []byte{2, 1, 'a'}
	if got := outputReport(CodexMicroProductID, report); !bytes.Equal(got, report) {
		t.Fatalf("Codex Micro report changed: %v", got)
	}
}
