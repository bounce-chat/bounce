.PHONY: windows windows-release
.PHONY: macos-release
.PHONY: linux-arch-release linux-debian-release linux-debian-release-docker
.PHONY: android-bind android android-release android-clean
.PHONY: clean
-include .env

ANDROID_SDK ?= $(HOME)/Android/sdk
ANDROID_NDK ?= $(lastword $(sort $(wildcard $(ANDROID_SDK)/ndk/*)))
NATIVE_ABIS ?= android/arm64,android/amd64
NATIVE_API ?= 29

# gomobile builds a library rather than a main package, so the toolchain does not
# stamp vcs.revision/vcs.modified the way `go build` does and the engine has no
# way to report which build is running. These pass the same two facts in by hand;
# every other target uses `go build` and needs none of this.
VCS_REVISION ?= $(shell git rev-parse HEAD 2>/dev/null)
VCS_MODIFIED ?= $(shell test -n "$$(git status --porcelain 2>/dev/null)" && echo true || echo false)
VERSION_LDFLAGS := -X github.com/bounce-chat/bounce/chat.buildRevision=$(VCS_REVISION) \
	-X github.com/bounce-chat/bounce/chat.buildModified=$(VCS_MODIFIED)

windows:
	CC=x86_64-w64-mingw32-gcc CGO_ENABLED=1 GOOS=windows go build -ldflags="-H windowsgui"

windows-release:
	CC=x86_64-w64-mingw32-gcc CGO_ENABLED=1 GOOS=windows fyne package --app-id chat.bounce -icon ui/assets/icon.png -name Bounce -os windows -tags migrated_fynedo --release
	mv Bounce.exe releases/Bounce.exe

macos-release:
	mkdir -p releases
	mkdir -p build
	rm -r build/*
	CGO_ENABLED=1 GOARCH=amd64 go build -tags migrated_fynedo -o build/bounce-amd64
	CGO_ENABLED=1 GOARCH=arm64 go build -tags migrated_fynedo -o build/bounce-arm64
	lipo -create -output build/bounce-universal build/bounce-arm64 build/bounce-amd64
	rm build/bounce-amd64
	rm build/bounce-arm64
	fyne package --app-id chat.bounce -icon ui/assets/icon.png -name Bounce -tags migrated_fynedo --release
	mv Bounce.app build/
	mv build/bounce-universal build/Bounce.app/Contents/MacOS/bounce
	codesign --deep --force --options runtime --sign "Developer ID Application: Hayden Parker (WLRMRLF7W6)" ./build/Bounce.app
	ditto -c -k --sequesterRsrc ./build/Bounce.app build/Bounce.zip
	xcrun notarytool submit build/Bounce.zip --key-id $(KEY_ID) --issuer $(ISSUER_ID) --key $(KEY_PATH) --wait
	xcrun stapler staple ./build/Bounce.app
	rm build/Bounce.zip
	ln -s /Applications build/Applications
	hdiutil create -volname "Bounce" -srcfolder "build/" -size 500m -ov -format UDZO "releases/Bounce.dmg"

linux-arch-release:
	rm -f pkg/*.tar.zst
	rm -rf pkg/bounce
	rm -rf pkg/bounce-fyne
	rm -rf pkg/bounce-fyne-tools
	cd pkg && makepkg --clean -f
	cd pkg && makepkg --clean -f -p PKGBUILD-bin
	mv pkg/*.tar.zst releases/
	rm -rf pkg/bounce
	rm -rf pkg/bounce-fyne
	rm -rf pkg/bounce-fyne-tools

linux-debian-release:
	rm -f bounce
	docker run --rm -it -v.:/go/src/bounce golang:trixie bash -c "cd src/bounce && make linux-debian-release-docker"

linux-debian-release-docker:
	apt update && apt install -y golang gcc libgl1-mesa-dev xorg-dev libwayland-dev libxkbcommon-dev
	git config --global --add safe.directory /go/src/bounce
	go build -tags migrated_fynedo
	rm -rf pkg/debian
	mkdir -p pkg/debian/bounce/DEBIAN
	mkdir -p pkg/debian/bounce/usr/local/bin
	mkdir -p pkg/debian/bounce/usr/share/applications
	mkdir -p pkg/debian/bounce/usr/share/icons/hicolor/scalable/apps
	mv bounce pkg/debian/bounce/usr/local/bin/bounce
	cp pkg/control pkg/debian/bounce/DEBIAN/
	cp pkg/bounce.desktop pkg/debian/bounce/usr/share/applications/
	cp ui/assets/icon.svg pkg/debian/bounce/usr/share/icons/hicolor/scalable/apps/bounce.svg
	cd pkg/debian && dpkg-deb --build bounce && chown 1000:1000 bounce.deb && mv bounce.deb ../../releases/bounce.deb && cd .. && rm -rf debian

android-bind:
	@test -d "$(ANDROID_NDK)" || (echo "No NDK found under $(ANDROID_SDK)/ndk. Set ANDROID_NDK=..." && false)
	mkdir -p android/app/libs
	ANDROID_HOME=$(ANDROID_SDK) ANDROID_NDK_HOME=$(ANDROID_NDK) \
		gomobile bind \
		-target=$(NATIVE_ABIS) \
		-androidapi $(NATIVE_API) \
		-javapkg chat.bounce \
		-o android/app/libs/goengine.aar \
		-ldflags="-checklinkname=0 $(VERSION_LDFLAGS)" \
		github.com/bounce-chat/bounce/android/goengine

android: android-bind
	cd android && ANDROID_HOME=$(ANDROID_SDK) ANDROID_SDK_ROOT=$(ANDROID_SDK) ./gradlew assembleDebug
	cp android/app/build/outputs/apk/debug/app-debug.apk ./Bounce.apk

android-release: android-bind
	mkdir -p releases
	cd android && ANDROID_HOME=$(ANDROID_SDK) ANDROID_SDK_ROOT=$(ANDROID_SDK) ./gradlew assembleRelease \
		-Pandroid.injected.signing.store.file=$(ANDROID_KEYSTORE_PATH) \
		-Pandroid.injected.signing.store.password=$(ANDROID_KEYSTORE_PASSWORD) \
		-Pandroid.injected.signing.key.alias=$(ANDROID_KEY_ALIAS) \
		-Pandroid.injected.signing.key.password=$(ANDROID_KEY_PASSWORD)
	cp android/app/build/outputs/apk/release/app-release.apk releases/Bounce.apk

android-clean:
	rm -rf android/app/libs/goengine.aar android/app/libs/goengine-sources.jar
	rm -rf android/app/build android/build android/.gradle
	rm -f Bounce.apk

clean: android-clean
	rm -r releases/*
	rm -r build/*
