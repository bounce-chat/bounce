//go:build android

#include <jni.h>
#include <stdbool.h>
#include <stdlib.h>
#include <string.h>

const char *batteryOptimizationsIgnored(uintptr_t java_vm, uintptr_t jni_env, uintptr_t ctx) {
	JNIEnv *env = (JNIEnv*)jni_env;

	jclass classNativeActivity = (*env)->FindClass(env, "android/app/NativeActivity");
	if (classNativeActivity == NULL) {
		return "android/app/NativeActivity not found";
	}
	jclass classPowerManager = (*env)->FindClass(env, "android/os/PowerManager");
	if (classPowerManager == NULL) {
		return "android/os/PowerManager not found";
	}

	jmethodID idNativeActivity_getSystemService = (*env)->GetMethodID(env, classNativeActivity, "getSystemService", "(Ljava/lang/String;)Ljava/lang/Object;");
	jstring stringPowerService = (*env)->NewStringUTF(env, "power");
	jobject powerManager = (*env)->CallObjectMethod(env, (jobject)ctx, idNativeActivity_getSystemService, stringPowerService);

	jmethodID idPowerManager_isIgnoringBatteryOptimizations = (*env)->GetMethodID(env, classPowerManager, "isIgnoringBatteryOptimizations", "(Ljava/lang/String;)Z");
	jstring stringBouncePackage = (*env)->NewStringUTF(env, "chat.bounce");
	jboolean isIgnoredBool = (*env)->CallBooleanMethod(env, powerManager, idPowerManager_isIgnoringBatteryOptimizations, stringBouncePackage);

	(*env)->DeleteLocalRef(env, stringBouncePackage);
	(*env)->DeleteLocalRef(env, stringPowerService);

	if (isIgnoredBool == JNI_TRUE) {
		return "ignored";
	}
	return "not ignored";
}

const char *requestIgnoreBatteryOptimizations(uintptr_t java_vm, uintptr_t jni_env, uintptr_t ctx) {
	JNIEnv *env = (JNIEnv*)jni_env;

	jclass classIntent = (*env)->FindClass(env, "android/content/Intent");
	if (classIntent == NULL) {
		return "android/content/Intent not found";
	}

	// Create an intent
	jmethodID idIntent_contructor = (*env)->GetMethodID(env, classIntent, "<init>", "()V");
	jobject intent = (*env)->NewObject(env, classIntent, idIntent_contructor);

	// Set the action on the intent
	jmethodID idIntent_setAction = (*env)->GetMethodID(env, classIntent, "setAction", "(Ljava/lang/String;)Landroid/content/Intent;");
	jstring stringRequestIgnoreBatteryOptimizations = (*env)->NewStringUTF(env, "android.settings.REQUEST_IGNORE_BATTERY_OPTIMIZATIONS");
	jobject intentWithAction = (*env)->CallObjectMethod(env, intent, idIntent_setAction, stringRequestIgnoreBatteryOptimizations);

	// Parse the package name as a URI
	jclass classUri = (*env)->FindClass(env, "android/net/Uri");
	if (classUri == NULL) {
		return "android/net/Uri not found";
	}
	jmethodID idUri_parse = (*env)->GetStaticMethodID(env, classUri, "parse", "(Ljava/lang/String;)Landroid/net/Uri;");
	jstring stringBouncePackage = (*env)->NewStringUTF(env, "package:chat.bounce");
	jobject parsedUriData = (*env)->CallStaticObjectMethod(env, classUri, idUri_parse, stringBouncePackage);

	// Set the URI as the intent data
	jmethodID idIntent_setData = (*env)->GetMethodID(env, classIntent, "setData", "(Landroid/net/Uri;)Landroid/content/Intent;");
	(*env)->CallObjectMethod(env, intent, idIntent_setData, parsedUriData);

	// Start the intent
	jclass classContext = (*env)->GetObjectClass(env, (jobject)ctx);
	if (classUri == NULL) {
		return "context class not found";
	}
	jmethodID idContext_startActivity = (*env)->GetMethodID(env, classContext, "startActivity", "(Landroid/content/Intent;)V");
	(*env)->CallVoidMethod(env, (jobject)ctx, idContext_startActivity, intentWithAction);

	// Clean up references
	(*env)->DeleteLocalRef(env, stringRequestIgnoreBatteryOptimizations);
	(*env)->DeleteLocalRef(env, stringBouncePackage);

	return "";
}

void updateSystemBarsAppearance(uintptr_t java_vm, uintptr_t jni_env, uintptr_t ctx, bool isDark) {
    JNIEnv *env = (JNIEnv*)jni_env;
    jboolean dark = isDark ? JNI_TRUE : JNI_FALSE;

    // 1. Get Build.VERSION.SDK_INT
    jclass versionClass = (*env)->FindClass(env, "android/os/Build$VERSION");
    jfieldID sdkIntField = (*env)->GetStaticFieldID(env, versionClass, "SDK_INT", "I");
    jint sdkInt = (*env)->GetStaticIntField(env, versionClass, sdkIntField);

    // Get current activity/context class to call getWindow()
    jclass activityClass = (*env)->GetObjectClass(env, (jobject)ctx);
    jmethodID getWindowMethod = (*env)->GetMethodID(env, activityClass, "getWindow", "()Landroid/view/Window;");
    jobject windowObj = (*env)->CallObjectMethod(env, (jobject)ctx, getWindowMethod);

    if (windowObj == NULL) return;

    // SDK_INT >= 30 (Android R)
    if (sdkInt >= 30) {
        jclass windowClass = (*env)->GetObjectClass(env, windowObj);
        jmethodID getInsetsControllerMethod = (*env)->GetMethodID(env, windowClass, "getInsetsController", "()Landroid/view/WindowInsetsController;");
        jobject controllerObj = (*env)->CallObjectMethod(env, windowObj, getInsetsControllerMethod);

        if (controllerObj != NULL) {
            jclass controllerClass = (*env)->GetObjectClass(env, controllerObj);
            jmethodID setSystemBarsAppearanceMethod = (*env)->GetMethodID(env, controllerClass, "setSystemBarsAppearance", "(II)V");

            // Look up appearance constants
            jclass wicClass = (*env)->FindClass(env, "android/view/WindowInsetsController");
            jfieldID lightStatusField = (*env)->GetStaticFieldID(env, wicClass, "APPEARANCE_LIGHT_STATUS_BARS", "I");
            jfieldID lightNavField = (*env)->GetStaticFieldID(env, wicClass, "APPEARANCE_LIGHT_NAVIGATION_BARS", "I");

            jint lightStatus = (*env)->GetStaticIntField(env, wicClass, lightStatusField);
            jint lightNav = (*env)->GetStaticIntField(env, wicClass, lightNavField);
            jint lightFlags = lightStatus | lightNav;

            jint appearance = dark ? 0 : lightFlags;
            (*env)->CallVoidMethod(env, controllerObj, setSystemBarsAppearanceMethod, appearance, lightFlags);

            (*env)->DeleteLocalRef(env, controllerClass);
            (*env)->DeleteLocalRef(env, wicClass);
            (*env)->DeleteLocalRef(env, controllerObj);
        }
        (*env)->DeleteLocalRef(env, windowClass);

    // SDK_INT >= 23 (Android M)
    } else if (sdkInt >= 23) {
        jclass windowClass = (*env)->GetObjectClass(env, windowObj);
        jmethodID getDecorViewMethod = (*env)->GetMethodID(env, windowClass, "getDecorView", "()Landroid/view/View;");
        jobject decorViewObj = (*env)->CallObjectMethod(env, windowObj, getDecorViewMethod);

        if (decorViewObj != NULL) {
            jclass viewClass = (*env)->GetObjectClass(env, decorViewObj);
            jmethodID getSysUiVisMethod = (*env)->GetMethodID(env, viewClass, "getSystemUiVisibility", "()I");
            jmethodID setSysUiVisMethod = (*env)->GetMethodID(env, viewClass, "setSystemUiVisibility", "(I)V");

            jint flags = (*env)->CallIntMethod(env, decorViewObj, getSysUiVisMethod);

            // Look up View flags constants
            jfieldID statusFlagID = (*env)->GetStaticFieldID(env, viewClass, "SYSTEM_UI_FLAG_LIGHT_STATUS_BAR", "I");
            jint statusFlag = (*env)->GetStaticIntField(env, viewClass, statusFlagID);

            jint navFlag = 0;
            if (sdkInt >= 26) { // Android O
                jfieldID navFlagID = (*env)->GetStaticFieldID(env, viewClass, "SYSTEM_UI_FLAG_LIGHT_NAVIGATION_BAR", "I");
                navFlag = (*env)->GetStaticIntField(env, viewClass, navFlagID);
            }

            if (dark) {
                flags &= ~statusFlag;
                if (sdkInt >= 26) {
                    flags &= ~navFlag;
                }
            } else {
                flags |= statusFlag;
                if (sdkInt >= 26) {
                    flags |= navFlag;
                }
            }

            (*env)->CallVoidMethod(env, decorViewObj, setSysUiVisMethod, flags);
            (*env)->DeleteLocalRef(env, viewClass);
            (*env)->DeleteLocalRef(env, decorViewObj);
        }
        (*env)->DeleteLocalRef(env, windowClass);
    }

    // Clean up remaining local references
    (*env)->DeleteLocalRef(env, versionClass);
    (*env)->DeleteLocalRef(env, activityClass);
    (*env)->DeleteLocalRef(env, windowObj);
}
