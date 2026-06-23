.PHONY: android
android:
	mkdir -p android/androidAPK/app/src/main/libs/
	mkdir -p android/androidAPK/app/src/main/jniLibs/
	cd android/service && go generate && cd ../..
	cd android/activity && go generate && cd ../..
	cd android/androidAPK && ANDROID_HOME=~/Android/sdk ANDROID_SDK_ROOT=~/Android/sdk gradle assembleDebug && cd ../..
	mv android/androidAPK/app/build/outputs/apk/debug/app-debug.apk ./Bounce.apk
