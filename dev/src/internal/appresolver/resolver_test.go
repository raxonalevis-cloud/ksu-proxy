package appresolver

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"ksu-proxy/internal/config"
)

func TestResolveWhitelistDerivesCloneUIDWhenPackageListForCloneUserMissesPackage(t *testing.T) {
	dir := t.TempDir()
	packagesList := filepath.Join(dir, "packages.list")
	dataUserDir := filepath.Join(dir, "user")
	if err := os.WriteFile(packagesList, []byte("com.google.android.apps.authenticator2 10367 0 /data/app/base.apk\ncom.other.app 10400 0 /data/app/base.apk\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dataUserDir, "999"), 0755); err != nil {
		t.Fatal(err)
	}

	r := Resolver{
		PackagesListPath: packagesList,
		DataUserDir:      dataUserDir,
		CloneUserIDs:     []int{999},
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			_ = ctx
			if name == "cmd" && len(args) >= 2 && args[0] == "user" && args[1] == "list" {
				return []byte("Users:\n\tUserInfo{0:Owner:13} running\n\tUserInfo{999:XSpace:30} running\n"), nil
			}
			if name == "cmd" && len(args) >= 7 && args[0] == "package" && args[3] == "--user" && args[4] == "0" {
				return []byte("package:/data/app/base.apk=com.google.android.apps.authenticator2 uid:10367\n"), nil
			}
			if name == "cmd" && len(args) >= 7 && args[0] == "package" && args[3] == "--user" && args[4] == "999" {
				return []byte("package:/data/app/base.apk=com.other.app uid:10400\n"), nil
			}
			return nil, os.ErrNotExist
		},
	}

	instances, err := r.ResolveWhitelist(context.Background(), []config.PackageRule{
		{PackageName: "com.google.android.apps.authenticator2", Scope: "all_instances"},
	})
	if err != nil {
		t.Fatal(err)
	}
	uids := UIDs(instances)
	if len(uids) != 2 {
		t.Fatalf("UIDs = %v", uids)
	}
	if uids[0] != 10367 || uids[1] != 99910367 {
		t.Fatalf("UIDs = %v", uids)
	}
}

func TestDiscoverUsersAddsDataUserDirectoryEntries(t *testing.T) {
	dir := t.TempDir()
	dataUserDir := filepath.Join(dir, "user")
	if err := os.MkdirAll(filepath.Join(dataUserDir, "999"), 0755); err != nil {
		t.Fatal(err)
	}
	r := Resolver{
		DataUserDir: dataUserDir,
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			_ = ctx
			return []byte("Users:\n\tUserInfo{0:Owner:13} running\n"), nil
		},
	}
	users := r.discoverUsers(context.Background())
	if !containsUser(users, 0) || !containsUser(users, 999) {
		t.Fatalf("users = %#v", users)
	}
}
