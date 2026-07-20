.PHONY: android windows
android:
	mkdir -p android/apk/app/src/main/libs/
	mkdir -p android/apk/app/src/main/jniLibs/
	cd android/service && go generate && cd ../..
	cd android/activity && go generate && cd ../..
	cd android/apk && ANDROID_HOME=~/Android/sdk ANDROID_SDK_ROOT=~/Android/sdk gradle assembleDebug && cd ../..
	mv android/apk/app/build/outputs/apk/debug/app-debug.apk ./Bounce.apk

windows:
	CC=x86_64-w64-mingw32-gcc CGO_ENABLED=1 GOOS=windows go build -ldflags="-H windowsgui"

clean:
	rm -r android/apk/app/src/main/jniLibs
	rm -r android/apk/app/build
	rm -r android/activity/unzippedAPK
