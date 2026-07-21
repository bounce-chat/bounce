.PHONY: android-generate android android-release windows windows-release
include .env

android-generate:
	mkdir -p android/apk/app/src/main/libs/
	mkdir -p android/apk/app/src/main/jniLibs/
	cd android/service && go generate && cd ../..
	cd android/activity && go generate && cd ../..

android: android-generate
	cd android/apk && ANDROID_HOME=~/Android/sdk ANDROID_SDK_ROOT=~/Android/sdk gradle assembleDebug && cd ../..
	mv android/apk/app/build/outputs/apk/debug/app-debug.apk ./Bounce.apk

android-release: android-generate
	mkdir -p releases
	cd android/apk && ANDROID_HOME=~/Android/sdk ANDROID_SDK_ROOT=~/Android/sdk gradle assembleRelease \
		-Pandroid.injected.signing.store.file=$(ANDROID_KEYSTORE_PATH) \
		-Pandroid.injected.signing.store.password=$(ANDROID_KEYSTORE_PASSWORD) \
		-Pandroid.injected.signing.key.alias=$(ANDROID_KEY_ALIAS) \
		-Pandroid.injected.signing.key.password=$(ANDROID_KEY_PASSWORD) \
	&& cd ../..
	mv android/apk/app/build/outputs/apk/release/app-release.apk ./releases/Bounce.apk

windows:
	CC=x86_64-w64-mingw32-gcc CGO_ENABLED=1 GOOS=windows go build -ldflags="-H windowsgui"

windows-release:
	CC=x86_64-w64-mingw32-gcc CGO_ENABLED=1 GOOS=windows fyne package --app-id chat.bounce -icon ui/assets/icon.png -name Bounce -os windows -tags migrated_fynedo --release
	mv Bounce.exe releases/Bounce.exe

clean:
	rm -r android/apk/app/src/main/jniLibs
	rm -r android/apk/app/build
	rm -r android/activity/unzippedAPK
	rm -r releases/*
