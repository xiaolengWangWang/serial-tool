#!/usr/bin/env bash
# Native macOS GUI or Linux CLI packaging; run on the target OS/architecture.
set -euo pipefail
cd "$(dirname "$0")/.."
version=$(sed -n 's/^const Version = "\([^"]*\)"/\1/p' core/modes.go)
test -n "$version"
test -z "$(git status --porcelain)" || { echo 'Source tree must be clean' >&2; exit 1; }
os=$(go env GOOS)
arch=$(go env GOARCH)
mkdir -p build/release
case "$os" in
  linux)
    output="build/release/commbox-linux-$arch"
    CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o "$output" ./apps/linux
    test "$("$output" -version)" = "$version"
    chmod 755 "$output"
    git show 'HEAD:docs/CommBox使用手册.md' > build/release/CommBox使用手册.md
    tar -czf "build/release/CommBox-$version-Linux-$arch.tar.gz" -C build/release "commbox-linux-$arch" 'CommBox使用手册.md'
    ;;
  darwin)
    case "$arch" in arm64) chip=AppleSilicon;; amd64) chip=Intel;; *) exit 1;; esac
    stage=$(mktemp -d "${TMPDIR:-/tmp}/commbox-package.XXXXXX")
    trap 'rm -rf -- "$stage"' EXIT
    app="$stage/CommBox.app"
    mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
    cp resources/Info.plist "$app/Contents/Info.plist"
    test "$(/usr/libexec/PlistBuddy -c 'Print CFBundleShortVersionString' "$app/Contents/Info.plist")" = "$version"
    cp resources/CommBox.icns "$app/Contents/Resources/CommBox.icns"
    git show 'HEAD:docs/CommBox使用手册.md' > "$app/Contents/Resources/CommBox使用手册.md"
    CGO_ENABLED=1 go build -trimpath -ldflags='-s -w' -o "$app/Contents/MacOS/CommBox" ./apps/macos
    codesign --force --deep --sign - "$app"
    codesign --verify --deep --strict "$app"
    ln -s /Applications "$stage/Applications"
    hdiutil create -volname "CommBox $version" -srcfolder "$stage" -ov -format UDZO "build/release/CommBox-$version-macOS-$chip.dmg"
    hdiutil verify "build/release/CommBox-$version-macOS-$chip.dmg"
    ;;
  *) echo "Unsupported native platform: $os" >&2; exit 1;;
esac
