package tui

import "testing"

func TestShowRAMNotice(t *testing.T) {
	m := &Model{}
	if m.ShowRAMNotice(false) {
		t.Fatal("shown without header")
	}
	m.hdrOK, m.hdr.StoreKind = true, "store-file"
	if m.ShowRAMNotice(false) || m.dlg != nil {
		t.Fatal("shown with a persistent store")
	}
	m.hdr.StoreKind = "ram"
	if !m.ShowRAMNotice(true) || m.dlg == nil || m.dlg.Title != "RAM mode" {
		t.Fatal("not shown in RAM mode")
	}
	if m.ShowRAMNotice(false) {
		t.Fatal("shown twice over an open dialog")
	}
}
