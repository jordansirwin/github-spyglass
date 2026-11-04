package notify

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation -framework UserNotifications -framework AppKit
#include <stdlib.h>
#include "native_darwin.h"
*/
import "C"

import (
	"fmt"
	"time"
	"unsafe"
)

// nativeAvailable reports whether the process runs inside an app bundle,
// which the UserNotifications framework requires.
func nativeAvailable() bool {
	return bool(C.spyglass_notify_available())
}

// nativeInit sets up the click-to-open delegate and requests permission.
func nativeInit() {
	C.spyglass_notify_init()
}

// nativeNotify posts a notification through Notification Center as this app.
func nativeNotify(title, subtitle, body, url string) {
	ident := fmt.Sprintf("github-spyglass-%d", time.Now().UnixNano())
	cIdent, cTitle, cSubtitle := C.CString(ident), C.CString(title), C.CString(subtitle)
	cBody, cURL := C.CString(body), C.CString(url)
	defer func() {
		C.free(unsafe.Pointer(cIdent))
		C.free(unsafe.Pointer(cTitle))
		C.free(unsafe.Pointer(cSubtitle))
		C.free(unsafe.Pointer(cBody))
		C.free(unsafe.Pointer(cURL))
	}()
	C.spyglass_notify(cIdent, cTitle, cSubtitle, cBody, cURL)
}
