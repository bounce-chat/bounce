//go:build android

package ui

import (
	"fyne.io/fyne/v2/driver"
	log "github.com/sirupsen/logrus"
)

/*
#include <stdlib.h>
#include <stdbool.h>

const char *batteryOptimizationsIgnored(uintptr_t java_vm, uintptr_t jni_env, uintptr_t ctx);
const char *requestIgnoreBatteryOptimizations(uintptr_t java_vm, uintptr_t jni_env, uintptr_t ctx);
*/
import "C"

func batteryOptimizationsIgnored() bool {
	var ignored bool
	driver.RunNative(func(ctx interface{}) error {
		ac := ctx.(*driver.AndroidContext)

		str := C.batteryOptimizationsIgnored(C.uintptr_t(ac.VM), C.uintptr_t(ac.Env), C.uintptr_t(ac.Ctx))
		goStr := C.GoString(str)

		ignored = goStr == "ignored"
		if !ignored && goStr != "not ignored" {
			log.WithFields(log.Fields{
				"error": goStr,
			}).Error("error checking if battery optimizations are ignored on android")
		}
		return nil
	})

	return ignored
}

func requestIgnoreBatteryOptimizations() {
	driver.RunNative(func(ctx interface{}) error {
		ac := ctx.(*driver.AndroidContext)

		str := C.requestIgnoreBatteryOptimizations(C.uintptr_t(ac.VM), C.uintptr_t(ac.Env), C.uintptr_t(ac.Ctx))
		goStr := C.GoString(str)
		if goStr != "" {
			log.WithFields(log.Fields{
				"error": goStr,
			}).Error("error starting intent to ignore battery optimizations")
		}

		return nil
	})
}
