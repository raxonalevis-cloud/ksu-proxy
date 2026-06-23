package appresolver

import "testing"

func TestParsePackageSpecWithSourceDir(t *testing.T) {
	pkg, sourceDir := parsePackageSpec("package:/data/app/base.apk=com.android.chrome")
	if pkg != "com.android.chrome" {
		t.Fatalf("pkg = %q", pkg)
	}
	if sourceDir != "/data/app/base.apk" {
		t.Fatalf("sourceDir = %q", sourceDir)
	}
}

func TestParsePackageSpecWithEqualsInSourceDir(t *testing.T) {
	pkg, sourceDir := parsePackageSpec("package:/data/app/~~qwe=/com.paradyme.solarsmash-vFU1P1RCjJ_7ptUauH5uMQ==/base.apk=com.paradyme.solarsmash")
	if pkg != "com.paradyme.solarsmash" {
		t.Fatalf("pkg = %q", pkg)
	}
	wantSource := "/data/app/~~qwe=/com.paradyme.solarsmash-vFU1P1RCjJ_7ptUauH5uMQ==/base.apk"
	if sourceDir != wantSource {
		t.Fatalf("sourceDir = %q", sourceDir)
	}
}

func TestParsePackageListOutputWithEqualsInSourceDir(t *testing.T) {
	raw := []byte("package:/data/app/~~qwe=/com.paradyme.solarsmash-vFU1P1RCjJ_7ptUauH5uMQ==/base.apk=com.paradyme.solarsmash uid:10367\n")
	apps := parsePackageListOutput(raw, 0)
	if len(apps) != 1 {
		t.Fatalf("len(apps) = %d", len(apps))
	}
	if apps[0].PackageName != "com.paradyme.solarsmash" {
		t.Fatalf("PackageName = %q", apps[0].PackageName)
	}
	if apps[0].UID != 10367 {
		t.Fatalf("UID = %d", apps[0].UID)
	}
}

func TestParsePackageListOutputNormalizesCloneUID(t *testing.T) {
	raw := []byte("package:/data/app/base.apk=com.google.android.apps.authenticator2 uid:10367\n")
	apps := parsePackageListOutput(raw, 999)
	if len(apps) != 1 {
		t.Fatalf("len(apps) = %d", len(apps))
	}
	if apps[0].UID != 99910367 {
		t.Fatalf("UID = %d", apps[0].UID)
	}
	if apps[0].AppID != 10367 {
		t.Fatalf("AppID = %d", apps[0].AppID)
	}
}

func TestParsePackageListOutputKeepsFullCloneUID(t *testing.T) {
	raw := []byte("package:/data/app/base.apk=com.google.android.apps.authenticator2 uid:99910367\n")
	apps := parsePackageListOutput(raw, 999)
	if len(apps) != 1 {
		t.Fatalf("len(apps) = %d", len(apps))
	}
	if apps[0].UID != 99910367 {
		t.Fatalf("UID = %d", apps[0].UID)
	}
	if apps[0].AppID != 10367 {
		t.Fatalf("AppID = %d", apps[0].AppID)
	}
}

func TestFillMissingCloneUIDsFromBase(t *testing.T) {
	raw := []byte("package:/data/app/base.apk=com.google.android.apps.authenticator2\n")
	apps := parsePackageListOutput(raw, 999)
	fillMissingUIDsFromBase(apps, map[string]int{"com.google.android.apps.authenticator2": 10367}, 999)
	if len(apps) != 1 {
		t.Fatalf("len(apps) = %d", len(apps))
	}
	if apps[0].UID != 99910367 {
		t.Fatalf("UID = %d", apps[0].UID)
	}
	if apps[0].AppID != 10367 {
		t.Fatalf("AppID = %d", apps[0].AppID)
	}
}

func TestDisplayNameUsesKnownNames(t *testing.T) {
	if got := DisplayName("com.tencent.mm", ""); got != "微信" {
		t.Fatalf("DisplayName = %q", got)
	}
	if got := DisplayName("com.openai.chatgpt", ""); got != "ChatGPT" {
		t.Fatalf("DisplayName = %q", got)
	}
	if got := DisplayName("com.microsoft.emmx", ""); got != "Edge" {
		t.Fatalf("DisplayName = %q", got)
	}
	if got := DisplayName("com.microsoft.emmx.beta", ""); got != "Edge Beta" {
		t.Fatalf("DisplayName = %q", got)
	}
}

func TestAvatarText(t *testing.T) {
	if got := AvatarText("微信", "com.tencent.mm"); got != "微" {
		t.Fatalf("AvatarText = %q", got)
	}
	if got := AvatarText("ChatGPT", "com.openai.chatgpt"); got != "C" {
		t.Fatalf("AvatarText = %q", got)
	}
}
