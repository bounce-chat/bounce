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
