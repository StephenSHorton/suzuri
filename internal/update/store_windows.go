//go:build windows

package update

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"github.com/charmbracelet/log"
	"golang.org/x/sys/windows"
)

const (
	roInitSingleThreaded = 0
	rpcEChangedMode      = 0x80010106
	sFalse               = 1

	asyncStarted   = 0
	asyncCompleted = 1
	asyncCanceled  = 2
	asyncError     = 3
)

var (
	modCombase = windows.NewLazySystemDLL("combase.dll")
	modUser32  = windows.NewLazySystemDLL("user32.dll")
	modShell32 = windows.NewLazySystemDLL("shell32.dll")

	procRoInitialize              = modCombase.NewProc("RoInitialize")
	procRoGetActivationFactory    = modCombase.NewProc("RoGetActivationFactory")
	procWindowsCreateString       = modCombase.NewProc("WindowsCreateString")
	procWindowsDeleteString       = modCombase.NewProc("WindowsDeleteString")
	procWindowsGetStringRawBuffer = modCombase.NewProc("WindowsGetStringRawBuffer")
	procEnumWindows               = modUser32.NewProc("EnumWindows")
	procGetWindowThreadProcessId  = modUser32.NewProc("GetWindowThreadProcessId")
	procIsWindowVisible           = modUser32.NewProc("IsWindowVisible")
	procGetForegroundWindow       = modUser32.NewProc("GetForegroundWindow")
	procGetAncestor               = modUser32.NewProc("GetAncestor")
	procShellExecuteW             = modShell32.NewProc("ShellExecuteW")
)

// Primitive WinRT IIDs (from Windows SDK winmd / headers).
var (
	iidIAsyncInfo            = parseGUID("00000036-0000-0000-c000-000000000046")
	iidIInitializeWithWindow = parseGUID("3e68d4bd-7135-4d10-8018-9fb6d9f33fa1")
	iidIStoreContext         = parseGUID("ac98b6be-f4fd-4912-babd-5035e5e8bcab")
	iidIStoreContext3        = parseGUID("e26226ca-1a01-4730-85a6-ecc896e4ae38")
	iidIStoreContextStatics  = parseGUID("9c06ee5f-15c0-4e72-9330-d6191cebd19c")
	iidIStorePackageUpdate   = parseGUID("140fa150-3cbf-4a35-b91f-48271c31b072")
	iidIPackage              = parseGUID("163c792f-bd75-413c-bf23-b1fe7b95d825")
	iidIPackageId            = parseGUID("1adb665e-37c7-4790-9980-dd7ae74e8bb2")
	iidIAsyncOperation       = parseGUID("9fc2b0bb-e446-44e2-aa61-9cab8f636af2")
	iidIVectorView           = parseGUID("bbe1fa4c-b0e3-4583-baef-1f1b2e483e56")
	iidIIterable             = parseGUID("faa585ea-6214-4217-afda-7f46de5869b3")
)

func storePackageUpdateRC() string {
	return rcSig("Windows.Services.Store.StorePackageUpdate", iidIStorePackageUpdate)
}

func iidIVectorViewStoreUpdate() guid {
	return pinterfaceGUID(pifaceSig(iidIVectorView, storePackageUpdateRC()))
}

func iidIIterableStoreUpdate() guid {
	return pinterfaceGUID(pifaceSig(iidIIterable, storePackageUpdateRC()))
}

func iidIAsyncOpVectorViewStoreUpdate() guid {
	inner := pifaceSig(iidIVectorView, storePackageUpdateRC())
	return pinterfaceGUID(pifaceSig(iidIAsyncOperation, inner))
}

type winrtObj struct{ p uintptr }

func (o winrtObj) ok() bool { return o.p != 0 }

func (o winrtObj) vtbl() *[32]uintptr {
	return *(**[32]uintptr)(unsafe.Pointer(o.p))
}

func (o winrtObj) addRef() {
	if o.p == 0 {
		return
	}
	_, _, _ = syscall.SyscallN(o.vtbl()[1], o.p)
}

func (o winrtObj) release() {
	if o.p == 0 {
		return
	}
	_, _, _ = syscall.SyscallN(o.vtbl()[2], o.p)
}

func (o winrtObj) query(id guid) (winrtObj, error) {
	var out uintptr
	hr, _, _ := syscall.SyscallN(o.vtbl()[0], o.p, uintptr(unsafe.Pointer(&id)), uintptr(unsafe.Pointer(&out)))
	if hr != 0 || out == 0 {
		return winrtObj{}, fmt.Errorf("queryinterface %s: 0x%x", id.string(), uint32(hr))
	}
	return winrtObj{p: out}, nil
}

func hrErr(hr uintptr) error {
	if hr == 0 || hr == sFalse {
		return nil
	}
	return fmt.Errorf("winrt hr=0x%x", uint32(hr))
}

func roInitialize() error {
	hr, _, _ := procRoInitialize.Call(uintptr(roInitSingleThreaded))
	if hr == 0 || hr == sFalse || uint32(hr) == rpcEChangedMode {
		return nil
	}
	return hrErr(hr)
}

func hstringFromGo(s string) (uintptr, error) {
	u16, err := windows.UTF16FromString(s)
	if err != nil {
		return 0, err
	}
	var hs uintptr
	n := uint32(len(u16) - 1)
	hr, _, _ := procWindowsCreateString.Call(
		uintptr(unsafe.Pointer(&u16[0])),
		uintptr(n),
		uintptr(unsafe.Pointer(&hs)),
	)
	if hr != 0 {
		return 0, hrErr(hr)
	}
	return hs, nil
}

func deleteHString(hs uintptr) {
	if hs != 0 {
		_, _, _ = procWindowsDeleteString.Call(hs)
	}
}

func hstringToGo(hs uintptr) string {
	if hs == 0 {
		return ""
	}
	var n uint32
	buf, _, _ := procWindowsGetStringRawBuffer.Call(hs, uintptr(unsafe.Pointer(&n)))
	if buf == 0 || n == 0 {
		return ""
	}
	return windows.UTF16ToString(unsafe.Slice((*uint16)(unsafe.Pointer(buf)), n))
}

func activationFactory(classID string, iid guid) (winrtObj, error) {
	hs, err := hstringFromGo(classID)
	if err != nil {
		return winrtObj{}, err
	}
	defer deleteHString(hs)
	var factory uintptr
	hr, _, _ := procRoGetActivationFactory.Call(
		hs,
		uintptr(unsafe.Pointer(&iid)),
		uintptr(unsafe.Pointer(&factory)),
	)
	if hr != 0 || factory == 0 {
		return winrtObj{}, fmt.Errorf("RoGetActivationFactory %s: 0x%x", classID, uint32(hr))
	}
	return winrtObj{p: factory}, nil
}

func storeContext(hwnd uintptr) (winrtObj, error) {
	statics, err := activationFactory("Windows.Services.Store.StoreContext", iidIStoreContextStatics)
	if err != nil {
		return winrtObj{}, err
	}
	defer statics.release()

	// IStoreContextStatics.GetDefault — IInspectable + 0
	var ctx uintptr
	hr, _, _ := syscall.SyscallN(statics.vtbl()[6], statics.p, uintptr(unsafe.Pointer(&ctx)))
	if hr != 0 || ctx == 0 {
		return winrtObj{}, fmt.Errorf("StoreContext.GetDefault: 0x%x", uint32(hr))
	}
	obj := winrtObj{p: ctx}
	if hwnd != 0 {
		if initw, qerr := obj.query(iidIInitializeWithWindow); qerr == nil {
			hr, _, _ = syscall.SyscallN(initw.vtbl()[3], initw.p, hwnd)
			initw.release()
			if hr != 0 {
				log.Debug("store: IInitializeWithWindow", "hr", fmt.Sprintf("0x%x", uint32(hr)))
			}
		}
	}
	return obj, nil
}

func waitAsync(obj winrtObj, timeout time.Duration) error {
	info, err := obj.query(iidIAsyncInfo)
	if err != nil {
		return err
	}
	defer info.release()

	deadline := time.Now().Add(timeout)
	for {
		var status uint32
		hr, _, _ := syscall.SyscallN(info.vtbl()[7], info.p, uintptr(unsafe.Pointer(&status)))
		if hr != 0 {
			return hrErr(hr)
		}
		switch status {
		case asyncCompleted:
			return nil
		case asyncCanceled:
			return ErrStoreCanceled
		case asyncError:
			var code uint32
			_, _, _ = syscall.SyscallN(info.vtbl()[8], info.p, uintptr(unsafe.Pointer(&code)))
			return fmt.Errorf("store async failed: 0x%x", code)
		}
		if time.Now().After(deadline) {
			return errors.New("store async timed out")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func asyncGetResults(obj winrtObj, iid guid) (winrtObj, error) {
	op, err := obj.query(iid)
	if err != nil {
		return winrtObj{}, err
	}
	defer op.release()
	var result uintptr
	hr, _, _ := syscall.SyscallN(op.vtbl()[8], op.p, uintptr(unsafe.Pointer(&result)))
	if hr != 0 || result == 0 {
		return winrtObj{}, fmt.Errorf("GetResults: 0x%x", uint32(hr))
	}
	return winrtObj{p: result}, nil
}

type packageVersion struct {
	Major, Minor, Build, Revision uint16
}

func (s *StoreService) Check() (*Info, error) {
	var info *Info
	err := s.onStoreThread(func() error {
		var err error
		info, err = s.checkLocked()
		return err
	})
	return info, err
}

func (s *StoreService) DownloadAndApply(_ Info) error {
	if s.OnApplyBegin != nil {
		s.OnApplyBegin()
	}
	return s.onStoreThread(s.installLocked)
}

func (s *StoreService) onStoreThread(fn func() error) error {
	errc := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		if err := roInitialize(); err != nil {
			errc <- err
			return
		}
		errc <- fn()
	}()
	return <-errc
}

func (s *StoreService) checkLocked() (*Info, error) {
	ctx, err := storeContext(findOwnerHWND(s.ownerPID))
	if err != nil {
		return nil, err
	}
	defer ctx.release()

	// IStoreContext.GetAppAndOptionalStorePackageUpdatesAsync — slot 23 (see windows-rs vtable)
	var async uintptr
	hr, _, _ := syscall.SyscallN(ctx.vtbl()[23], ctx.p, uintptr(unsafe.Pointer(&async)))
	if hr != 0 || async == 0 {
		return nil, fmt.Errorf("GetAppAndOptionalStorePackageUpdatesAsync: 0x%x", uint32(hr))
	}
	op := winrtObj{p: async}
	defer op.release()
	if err := waitAsync(op, 2*time.Minute); err != nil {
		return nil, err
	}
	view, err := asyncGetResults(op, iidIAsyncOpVectorViewStoreUpdate())
	if err != nil {
		return nil, err
	}
	defer view.release()

	var n uint32
	hr, _, _ = syscall.SyscallN(view.vtbl()[7], view.p, uintptr(unsafe.Pointer(&n)))
	if hr != 0 {
		return nil, hrErr(hr)
	}
	if n == 0 {
		return nil, nil
	}

	var first uintptr
	hr, _, _ = syscall.SyscallN(view.vtbl()[6], view.p, 0, uintptr(unsafe.Pointer(&first)))
	if hr != 0 || first == 0 {
		return nil, fmt.Errorf("IVectorView.GetAt: 0x%x", uint32(hr))
	}
	upd := winrtObj{p: first}
	defer upd.release()

	ver, err := storeUpdateVersion(upd)
	if err != nil {
		return nil, err
	}
	return &Info{
		Version:   DisplayVersion(ver.Major, ver.Minor, ver.Build, ver.Revision),
		Notes:     "Microsoft Store",
		AssetName: "ms-store",
		AssetURL:  "ms-windows-store://pdp/?productid=" + StoreProductID,
	}, nil
}

func storeUpdateVersion(upd winrtObj) (packageVersion, error) {
	pkgObj, err := upd.query(iidIStorePackageUpdate)
	if err != nil {
		// already the default interface
		pkgObj = upd
		pkgObj.addRef()
	}
	defer pkgObj.release()

	var pkg uintptr
	hr, _, _ := syscall.SyscallN(pkgObj.vtbl()[6], pkgObj.p, uintptr(unsafe.Pointer(&pkg)))
	if hr != 0 || pkg == 0 {
		return packageVersion{}, fmt.Errorf("StorePackageUpdate.Package: 0x%x", uint32(hr))
	}
	packageObj := winrtObj{p: pkg}
	defer packageObj.release()

	ipkg, err := packageObj.query(iidIPackage)
	if err != nil {
		ipkg = packageObj
		ipkg.addRef()
	}
	defer ipkg.release()

	var idp uintptr
	hr, _, _ = syscall.SyscallN(ipkg.vtbl()[6], ipkg.p, uintptr(unsafe.Pointer(&idp)))
	if hr != 0 || idp == 0 {
		return packageVersion{}, fmt.Errorf("Package.Id: 0x%x", uint32(hr))
	}
	idObj := winrtObj{p: idp}
	defer idObj.release()

	iid, err := idObj.query(iidIPackageId)
	if err != nil {
		iid = idObj
		iid.addRef()
	}
	defer iid.release()

	var ver packageVersion
	hr, _, _ = syscall.SyscallN(iid.vtbl()[7], iid.p, uintptr(unsafe.Pointer(&ver)))
	if hr != 0 {
		return packageVersion{}, fmt.Errorf("PackageId.Version: 0x%x", uint32(hr))
	}
	return ver, nil
}

func (s *StoreService) installLocked() error {
	hwnd := findOwnerHWND(s.ownerPID)
	ctx, err := storeContext(hwnd)
	if err != nil {
		_ = openStorePage()
		return err
	}
	defer ctx.release()

	var async uintptr
	hr, _, _ := syscall.SyscallN(ctx.vtbl()[23], ctx.p, uintptr(unsafe.Pointer(&async)))
	if hr != 0 || async == 0 {
		_ = openStorePage()
		return fmt.Errorf("GetAppAndOptionalStorePackageUpdatesAsync: 0x%x", uint32(hr))
	}
	listOp := winrtObj{p: async}
	defer listOp.release()
	if err := waitAsync(listOp, 2*time.Minute); err != nil {
		return err
	}
	view, err := asyncGetResults(listOp, iidIAsyncOpVectorViewStoreUpdate())
	if err != nil {
		return err
	}
	defer view.release()

	var n uint32
	hr, _, _ = syscall.SyscallN(view.vtbl()[7], view.p, uintptr(unsafe.Pointer(&n)))
	if hr != 0 {
		return hrErr(hr)
	}
	if n == 0 {
		return errors.New("no store package updates")
	}

	iterable, err := view.query(iidIIterableStoreUpdate())
	if err != nil {
		// IVectorView<T> is IIterable<T>; try passing the view directly.
		iterable = view
		iterable.addRef()
	}
	defer iterable.release()

	if err := s.trySilentInstall(ctx, iterable); err == nil {
		return nil
	} else {
		log.Info("store: silent install unavailable, requesting UI", "err", err)
	}

	// IStoreContext.RequestDownloadAndInstallStorePackageUpdatesAsync — slot 25
	var instAsync uintptr
	hr, _, _ = syscall.SyscallN(ctx.vtbl()[25], ctx.p, iterable.p, uintptr(unsafe.Pointer(&instAsync)))
	if hr != 0 || instAsync == 0 {
		_ = openStorePage()
		return fmt.Errorf("RequestDownloadAndInstallStorePackageUpdatesAsync: 0x%x", uint32(hr))
	}
	inst := winrtObj{p: instAsync}
	defer inst.release()
	if err := waitAsync(inst, 15*time.Minute); err != nil {
		if errors.Is(err, ErrStoreCanceled) {
			return err
		}
		_ = openStorePage()
		return err
	}
	return nil
}

func (s *StoreService) trySilentInstall(ctx, iterable winrtObj) error {
	ctx3, err := ctx.query(iidIStoreContext3)
	if err != nil {
		return err
	}
	defer ctx3.release()

	var can bool
	hr, _, _ := syscall.SyscallN(ctx3.vtbl()[6], ctx3.p, uintptr(unsafe.Pointer(&can)))
	if hr != 0 || !can {
		return fmt.Errorf("CanSilentlyDownloadStorePackageUpdates hr=0x%x can=%v", uint32(hr), can)
	}

	// TrySilentDownloadAndInstallStorePackageUpdatesAsync — slot 8 (inspectable + 2)
	var async uintptr
	hr, _, _ = syscall.SyscallN(ctx3.vtbl()[8], ctx3.p, iterable.p, uintptr(unsafe.Pointer(&async)))
	if hr != 0 || async == 0 {
		return fmt.Errorf("TrySilentDownloadAndInstall: 0x%x", uint32(hr))
	}
	op := winrtObj{p: async}
	defer op.release()
	return waitAsync(op, 15*time.Minute)
}

func openStorePage() error {
	uri, err := windows.UTF16PtrFromString("ms-windows-store://pdp/?productid=" + StoreProductID)
	if err != nil {
		return err
	}
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	r, _, _ := procShellExecuteW.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(uri)), 0, 0, uintptr(windows.SW_SHOWNORMAL))
	if r <= 32 {
		return fmt.Errorf("ShellExecute store page: %d", r)
	}
	return nil
}

const gaRoot = 2

type enumArg struct {
	wantPID uint32
	alsoPID uint32
	hwnd    uintptr
}

func enumWindowsProc(hwnd, lparam uintptr) uintptr {
	arg := (*enumArg)(unsafe.Pointer(lparam))
	var pid uint32
	_, _, _ = procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid != arg.wantPID && pid != arg.alsoPID {
		return 1
	}
	vis, _, _ := procIsWindowVisible.Call(hwnd)
	if vis == 0 {
		return 1
	}
	root, _, _ := procGetAncestor.Call(hwnd, uintptr(gaRoot))
	if root != 0 {
		hwnd = root
	}
	arg.hwnd = hwnd
	return 0
}

var enumWindowsCB = syscall.NewCallback(enumWindowsProc)

func findOwnerHWND(pid int) uintptr {
	arg := enumArg{
		wantPID: uint32(pid),
		alsoPID: uint32(os.Getpid()),
	}
	if arg.wantPID == 0 {
		arg.wantPID = arg.alsoPID
	}
	_, _, _ = procEnumWindows.Call(enumWindowsCB, uintptr(unsafe.Pointer(&arg)))
	if arg.hwnd != 0 {
		return arg.hwnd
	}
	fg, _, _ := procGetForegroundWindow.Call()
	return fg
}
