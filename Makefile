.PHONY: setup dev check build android-apk

setup:
	./.agents/setup

dev:
	./scripts/dev

check:
	mise run check

build:
	mise exec -- aube run build
	mise exec -- go build ./cmd/nimotsu

android-apk:
	mise exec -- aube run android:apk
