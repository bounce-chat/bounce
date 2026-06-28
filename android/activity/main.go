//go:generate go get golang.org/x/mobile/bind
//go:generate go install golang.org/x/mobile/cmd/gomobile@latest
//go:generate go install golang.org/x/mobile/cmd/gobind@latest
//go:generate gomobile init

////go:generate fyne package  --app-id chat.bounce -icon ../../ui/assets/launcher_circle.png -name Bounce -os android -tags migrated_fynedo
//go:generate ../../../tools/cmd/fyne/fyne package  --app-id chat.bounce -icon ../../ui/assets/launcher_circle.png -name Bounce -os android -tags migrated_fynedo

//go:generate unzip -o "Bounce.apk" -d ./unzippedAPK
//https://github.com/pxb1988/dex2jar/releases
//go:generate dex2jar ./unzippedAPK/classes.dex -o ./activity.jar --force

//go:generate zip -d activity.jar "com/google/errorprone/*" "com/google/common/*" "org/jspecify/*" "org/jetbrains/*" "org/intellij/*" "kotlinx/*" "kotlin/*" "android/*" "androidx/annotation/*" "androidx/arch/*" "androidx/collection/*" "androidx/concurrent/*" "androidx/core/*" "androidx/lifecycle/*" "androidx/tracing/*" "androidx/versionedparcelable/*"
//go:generate sh -c "cp activity.jar ../apk/app/src/main/libs"
//go:generate sh -c "cp -r unzippedAPK/lib/* ../apk/app/src/main/jniLibs/"

package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"
	"unicode/utf16"
	"unsafe"

	"fyne.io/fyne/v2/driver"

	"github.com/AndroidGoLab/jni"
	jniApp "github.com/AndroidGoLab/jni/app"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"

	"github.com/hkparker/bounce/ui"
)

var binderRef *jni.Object
var binderVM *jni.VM
var binderMu sync.Mutex
var boundCh = make(chan struct{}, 1)

func main() {
	startGoForegroundService()

	bind := func() {
		// Give the window time to initialize the native context
		time.Sleep(500 * time.Millisecond)

		log.Println("Attempting to bind to service...")
		if err := bindToService(); err != nil {
			log.Println("bindToService error:", err)
			return
		}

		// Wait for onServiceConnected
		select {
		case <-boundCh:
			log.Println("Service binding confirmed")
		case <-time.After(10 * time.Second):
			log.Println("timed out waiting for service binding")
		}

		err := requestPostNotificationPermission()
		if err != nil {
			log.Println("error posting notification: ", err)
		}
	}

	ui.StartShimmed(&aidlBounce{}, bind, pollEvents)
}

func startGoForegroundService() error {
	return driver.RunNative(func(ctx interface{}) error {
		ac := ctx.(*driver.AndroidContext)
		env := jni.EnvFromUintptr(ac.Env)
		activity := jni.ObjectFromUintptr(ac.Ctx)

		if err := jniApp.Init(env); err != nil {
			return fmt.Errorf("app.Init: %w", err)
		}

		// Find GoForegroundService class via class loader
		actCls := env.GetObjectClass(activity)
		getClMid, err := env.GetMethodID(actCls, "getClassLoader", "()Ljava/lang/ClassLoader;")
		if err != nil {
			return err
		}
		classLoader, err := env.CallObjectMethod(activity, getClMid)
		if err != nil {
			return err
		}
		clCls := env.GetObjectClass(classLoader)
		loadMid, err := env.GetMethodID(clCls, "loadClass", "(Ljava/lang/String;)Ljava/lang/Class;")
		if err != nil {
			return err
		}
		clsName, err := env.NewStringUTF("chat.bounce.GoForegroundService")
		if err != nil {
			return err
		}
		serviceClass, err := env.CallObjectMethod(classLoader, loadMid, jni.ObjectValue(&clsName.Object))
		if err != nil {
			return fmt.Errorf("loadClass: %w", err)
		}

		// Create Intent(Context, Class)
		intentCls, err := env.FindClass("android/content/Intent")
		if err != nil {
			return err
		}
		intentInit, err := env.GetMethodID(intentCls, "<init>", "(Landroid/content/Context;Ljava/lang/Class;)V")
		if err != nil {
			return err
		}
		intent, err := env.NewObject(intentCls, intentInit,
			jni.ObjectValue(activity), jni.ObjectValue(serviceClass))
		if err != nil {
			return fmt.Errorf("new Intent: %w", err)
		}

		// Call startForegroundService(intent)
		ctxCls, err := env.FindClass("android/content/Context")
		if err != nil {
			return err
		}
		startMid, err := env.GetMethodID(ctxCls, "startForegroundService", "(Landroid/content/Intent;)Landroid/content/ComponentName;")
		if err != nil {
			return err
		}
		_, err = env.CallObjectMethod(activity, startMid, jni.ObjectValue(intent))
		if err != nil {
			return fmt.Errorf("startForegroundService: %w", err)
		}

		log.Println("GoForegroundService started from Go")
		return nil
	})
}

func requestPostNotificationPermission() error {
	return driver.RunNative(func(ctx interface{}) error {
		ac := ctx.(*driver.AndroidContext)
		env := jni.EnvFromUintptr(ac.Env)

		// Wrap Fyne's context (the Activity) as a jni.Object
		activity := jni.ObjectFromUintptr(ac.Ctx)

		actCls := env.GetObjectClass(activity)

		reqMid, err := env.GetMethodID(actCls, "requestPermissions", "([Ljava/lang/String;I)V")
		if err != nil {
			log.Println("requestPermissions method not found: ", err)
			return err
		}

		strCls, err := env.FindClass("java/lang/String")
		if err != nil {
			return err
		}

		perms := []string{"android.permission.POST_NOTIFICATIONS"}
		arr, err := env.NewObjectArray(int32(len(perms)), strCls, nil)
		if err != nil {
			return err
		}

		for i, p := range perms {
			jP, err := env.NewStringUTF(p)
			if err != nil {
				return err
			}
			_ = env.SetObjectArrayElement(arr, int32(i), &jP.Object)
		}

		_ = env.CallVoidMethod(activity, reqMid,
			jni.ObjectValue(&arr.Object), jni.IntValue(1))

		return nil
	})
}

// bindToService binds to GoForegroundService via AIDL and stores the binder proxy.
// Must be called from inside driver.RunNative or on a thread with a valid JNI env.
func bindToService() error {
	return driver.RunNative(func(ctx interface{}) error {
		ac := ctx.(*driver.AndroidContext)
		vm := jni.VMFromUintptr(ac.VM)
		env := jni.EnvFromUintptr(ac.Env)
		activity := jni.ObjectFromUintptr(ac.Ctx)

		binderVM = vm

		// Set up proxy ClassLoader for EnsureProxyInit
		actCls := env.GetObjectClass(activity)
		getClMid, _ := env.GetMethodID(actCls, "getClassLoader", "()Ljava/lang/ClassLoader;")
		classLoader, _ := env.CallObjectMethod(activity, getClMid)
		clGlobal := env.NewGlobalRef(classLoader)
		jni.SetProxyClassLoader(clGlobal)

		if err := jni.EnsureProxyInit(env); err != nil {
			return fmt.Errorf("EnsureProxyInit: %w", err)
		}

		// Find ServiceConnection interface
		scCls, err := env.FindClass("android/content/ServiceConnection")
		if err != nil {
			return fmt.Errorf("find ServiceConnection: %w", err)
		}

		// Create proxy for ServiceConnection
		proxy, _, err := env.NewProxy(
			[]*jni.Class{(*jni.Class)(unsafe.Pointer(scCls))},
			func(env *jni.Env, methodName string, args []*jni.Object) (*jni.Object, error) {
				switch methodName {
				case "onServiceConnected":
					// args[0] = ComponentName, args[1] = IBinder
					ibinder := args[1]

					// Call IGoService.Stub.asInterface(ibinder) to get the AIDL proxy
					stubCls, err := env.FindClass("chat/bounce/IGoService$Stub")
					if err != nil {
						log.Println("IGoService.Stub not found:", err)
						return nil, nil
					}
					asIfaceMid, err := env.GetStaticMethodID(
						stubCls, "asInterface",
						"(Landroid/os/IBinder;)Lchat/bounce/IGoService;")
					if err != nil {
						log.Println("asInterface not found:", err)
						return nil, nil
					}
					aidlProxy, err := env.CallStaticObjectMethod(stubCls, asIfaceMid,
						jni.ObjectValue(ibinder))
					if err != nil {
						log.Println("asInterface failed:", err)
						return nil, nil
					}

					binderMu.Lock()
					binderRef = env.NewGlobalRef(aidlProxy)
					binderMu.Unlock()

					log.Println("AIDL service bound successfully")

					// Signal that binding is ready
					select {
					case boundCh <- struct{}{}:
					default:
					}

				case "onServiceDisconnected":
					binderMu.Lock()
					if binderRef != nil {
						vm.Do(func(env *jni.Env) error {
							env.DeleteGlobalRef(binderRef)
							return nil
						})
						binderRef = nil
					}
					binderMu.Unlock()
					log.Println("AIDL service disconnected")
				}
				return nil, nil
			},
		)
		if err != nil {
			return fmt.Errorf("NewProxy ServiceConnection: %w", err)
		}

		// Store proxy as global ref so it survives
		proxyGlobal := env.NewGlobalRef(proxy)

		// Create Intent targeting GoForegroundService
		intentCls, _ := env.FindClass("android/content/Intent")
		initMid, _ := env.GetMethodID(intentCls, "<init>", "()V")
		intent, _ := env.NewObject(intentCls, initMid)

		setClassMid, _ := env.GetMethodID(intentCls, "setClassName",
			"(Ljava/lang/String;Ljava/lang/String;)Landroid/content/Intent;")
		jPkg, _ := env.NewStringUTF("chat.bounce")
		jCls, _ := env.NewStringUTF("chat.bounce.GoForegroundService")
		env.CallObjectMethod(intent, setClassMid,
			jni.ObjectValue(&jPkg.Object), jni.ObjectValue(&jCls.Object))

		// bindService(intent, conn, BIND_AUTO_CREATE)
		ctxCls, _ := env.FindClass("android/content/Context")
		bindMid, _ := env.GetMethodID(ctxCls, "bindService",
			"(Landroid/content/Intent;Landroid/content/ServiceConnection;I)Z")

		const BIND_AUTO_CREATE = 1
		_, _ = env.CallBooleanMethod(activity, bindMid,
			jni.ObjectValue(intent),
			jni.ObjectValue(proxyGlobal),
			jni.IntValue(BIND_AUTO_CREATE))

		log.Println("bindService called, waiting for onServiceConnected...")
		return nil
	})
}

// callStringMethod calls a no-arg method that returns String on the AIDL binder proxy
func callStringMethod(methodName string) (string, error) {
	binderMu.Lock()
	ref := binderRef
	vm := binderVM
	binderMu.Unlock()

	if ref == nil || vm == nil {
		return "", fmt.Errorf("service not bound")
	}

	var result string
	err := vm.Do(func(env *jni.Env) error {
		cls := env.GetObjectClass(ref)
		mid, err := env.GetMethodID(cls, methodName, "()Ljava/lang/String;")
		if err != nil {
			return fmt.Errorf("%s not found: %w", methodName, err)
		}
		obj, err := env.CallObjectMethod(ref, mid)
		if err != nil {
			return err
		}
		if obj != nil {
			result = env.GoString((*jni.String)(unsafe.Pointer(obj)))
		}
		return nil
	})
	return result, err
}

func callServiceFunction(rawArg string) (string, error) {
	arg := base64.StdEncoding.EncodeToString([]byte(rawArg))

	binderMu.Lock()
	ref := binderRef
	vm := binderVM
	binderMu.Unlock()

	if ref == nil || vm == nil {
		return "", fmt.Errorf("service not bound")
	}

	var result string
	err := vm.Do(func(env *jni.Env) error {
		cls := env.GetObjectClass(ref)
		mid, err := env.GetMethodID(cls, "eval", "(Ljava/lang/String;)Ljava/lang/String;")
		if err != nil {
			return fmt.Errorf("eval method not found: %w", err)
		}

		jStr, err := env.NewString(utf16.Encode([]rune(arg)))
		if err != nil {
			return fmt.Errorf("cannot make java string: %w", err)
		}
		jObj := (*jni.Object)(unsafe.Pointer(jStr))

		retObj, err := env.CallObjectMethod(ref, mid, jni.ObjectValue(jObj))
		if err != nil {
			return err
		}
		if retObj != nil {
			result = env.GoString((*jni.String)(unsafe.Pointer(retObj)))
		}
		return nil
	})
	return result, err
}

func getConfigDirectory() string {
	var configDirectory string
	if testing.Testing() {
		configDirectory = os.TempDir() + "/bounce-test-" + uuid.New().String()
	} else if runtime.GOOS == "android" {
		// TODO: use /data/data/chat.bounce/ ?
		configDirectory = "/sdcard/Android/data/chat.bounce"
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			log.WithFields(log.Fields{
				"at":    "configDirectory",
				"error": err.Error(),
			}).Fatal("error getting home directory")
		}
		configDirectory = home + "/.bounce"
	}

	err := os.MkdirAll(configDirectory+"/blobs/", 0700)
	if err != nil {
		log.WithFields(log.Fields{
			"path":  configDirectory,
			"error": err.Error(),
		}).Fatal("error creating config directory")
	}

	return configDirectory
}
