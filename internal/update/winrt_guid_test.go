package update

import "testing"

func TestPInterfaceGUID_IAsyncOperationString(t *testing.T) {
	// windows.foundation.h: IAsyncOperation<HSTRING>
	// MIDL_INTERFACE("3e1fe603-f897-5263-b328-0806426b8a79")
	iidIAsyncOp := parseGUID("9fc2b0bb-e446-44e2-aa61-9cab8f636af2")
	got := pinterfaceGUID(pifaceSig(iidIAsyncOp, "string"))
	want := "3e1fe603-f897-5263-b328-0806426b8a79"
	if got.string() != want {
		t.Fatalf("got %s want %s", got.string(), want)
	}
}

func TestPInterfaceGUID_IVectorViewString(t *testing.T) {
	// IVectorView<HSTRING> = 2f13c006-a03a-5f69-b090-75a43e33423e
	iidIVec := parseGUID("bbe1fa4c-b0e3-4583-baef-1f1b2e483e56")
	got := pinterfaceGUID(pifaceSig(iidIVec, "string"))
	want := "2f13c006-a03a-5f69-b090-75a43e33423e"
	if got.string() != want {
		t.Fatalf("got %s want %s", got.string(), want)
	}
}

func TestDisplayVersion(t *testing.T) {
	if got := DisplayVersion(1, 9, 116, 0); got != "0.9.116" {
		t.Fatalf("store rewrite: got %q", got)
	}
	if got := DisplayVersion(1, 9, 105, 0); got != "0.9.105" {
		t.Fatalf("store rewrite 105: got %q", got)
	}
	if got := DisplayVersion(2, 0, 1, 0); got != "2.0.1" {
		t.Fatalf("major 2: got %q", got)
	}
	if got := DisplayVersion(1, 2, 3, 4); got != "1.2.3.4" {
		t.Fatalf("revision kept: got %q", got)
	}
}

func TestParseMSIXVersion(t *testing.T) {
	maj, min, bld, rev, ok := ParseMSIXVersion("1.9.116.0")
	if !ok || maj != 1 || min != 9 || bld != 116 || rev != 0 {
		t.Fatalf("%d.%d.%d.%d ok=%v", maj, min, bld, rev, ok)
	}
	if _, _, _, _, ok := ParseMSIXVersion("nope"); ok {
		t.Fatal("expected parse fail")
	}
}
