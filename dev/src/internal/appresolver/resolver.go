package appresolver

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"ksu-proxy/internal/config"
)

const androidUserOffset = 100000

type CommandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

type Resolver struct {
	Run              CommandRunner
	PackagesListPath string
	DataUserDir      string
	CloneUserIDs     []int
}

type UserProfile struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Alive bool   `json:"alive"`
}

type AppInstance struct {
	PackageName string `json:"package_name"`
	UserID      int    `json:"user_id"`
	UID         int    `json:"uid"`
	AppID       int    `json:"app_id"`
	Label       string `json:"label,omitempty"`
	SourceDir   string `json:"source_dir,omitempty"`
	Source      string `json:"source"`
}

type ListOptions struct {
	ResolveLabels bool
}

func DefaultRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func New(cloneUserIDs []int) Resolver {
	return Resolver{
		Run:              DefaultRunner,
		PackagesListPath: "/data/system/packages.list",
		DataUserDir:      "/data/user",
		CloneUserIDs:     cloneUserIDs,
	}
}

func (r Resolver) ResolveWhitelist(ctx context.Context, rules []config.PackageRule) ([]AppInstance, error) {
	if r.Run == nil {
		r.Run = DefaultRunner
	}
	if r.PackagesListPath == "" {
		r.PackagesListPath = "/data/system/packages.list"
	}
	if r.DataUserDir == "" {
		r.DataUserDir = "/data/user"
	}

	base, _ := r.readPackagesList()
	users := r.discoverUsers(ctx)
	if len(users) == 0 {
		users = []UserProfile{{ID: 0, Name: "owner", Kind: "main", Alive: true}}
	}
	users = appendConfiguredCloneUsers(users, r.CloneUserIDs)

	byPackageUser := make(map[string]AppInstance)
	for _, user := range users {
		items, err := r.listPackagesForUser(ctx, user.ID, base)
		if err != nil && len(base) > 0 && (user.ID == 0 || containsInt(r.CloneUserIDs, user.ID)) {
			items = fallbackInstancesForUser(base, user.ID)
		}
		for _, item := range items {
			key := instanceKey(item.PackageName, item.UserID)
			byPackageUser[key] = item
		}
	}
	addDerivedUserInstances(byPackageUser, rules, users, base)

	out := make([]AppInstance, 0)
	for _, rule := range rules {
		for _, item := range byPackageUser {
			if item.PackageName != rule.PackageName {
				continue
			}
			if !ruleAllowsUser(rule, item.UserID) {
				continue
			}
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PackageName == out[j].PackageName {
			return out[i].UID < out[j].UID
		}
		return out[i].PackageName < out[j].PackageName
	})
	return uniqueInstances(out), nil
}

func (r Resolver) ListInstalled(ctx context.Context) ([]AppInstance, error) {
	return r.ListInstalledWithOptions(ctx, ListOptions{ResolveLabels: true})
}

func (r Resolver) ListInstalledWithOptions(ctx context.Context, options ListOptions) ([]AppInstance, error) {
	if r.Run == nil {
		r.Run = DefaultRunner
	}
	if r.DataUserDir == "" {
		r.DataUserDir = "/data/user"
	}
	base, _ := r.readPackagesList()
	users := r.discoverUsers(ctx)
	if len(users) == 0 {
		users = []UserProfile{{ID: 0, Name: "owner", Kind: "main", Alive: true}}
	}
	users = appendConfiguredCloneUsers(users, r.CloneUserIDs)
	out := make([]AppInstance, 0)
	for _, user := range users {
		items, err := r.listPackagesForUser(ctx, user.ID, base)
		if err != nil && len(base) > 0 && (user.ID == 0 || containsInt(r.CloneUserIDs, user.ID)) {
			items = fallbackInstancesForUser(base, user.ID)
		}
		out = append(out, items...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PackageName == out[j].PackageName {
			return out[i].UID < out[j].UID
		}
		return out[i].PackageName < out[j].PackageName
	})
	out = uniqueInstances(out)
	if options.ResolveLabels {
		out = r.fillLabels(ctx, out)
	}
	return out, nil
}

func (r Resolver) discoverUsers(ctx context.Context) []UserProfile {
	raw, err := r.Run(ctx, "cmd", "user", "list")
	if err != nil {
		return r.discoverDataUsers()
	}
	re := regexp.MustCompile(`UserInfo\{([0-9]+):([^:}]+):[^}]*\}`)
	matches := re.FindAllSubmatch(raw, -1)
	users := make([]UserProfile, 0, len(matches))
	for _, match := range matches {
		id, _ := strconv.Atoi(string(match[1]))
		name := string(match[2])
		kind := "profile"
		if id == 0 {
			kind = "main"
		} else if strings.Contains(strings.ToLower(name), "clone") || id >= 900 {
			kind = "clone"
		}
		users = append(users, UserProfile{ID: id, Name: name, Kind: kind, Alive: true})
	}
	users = appendDataUsers(users, r.discoverDataUsers())
	return users
}

func (r Resolver) discoverDataUsers() []UserProfile {
	if r.DataUserDir == "" {
		r.DataUserDir = "/data/user"
	}
	entries, err := os.ReadDir(r.DataUserDir)
	if err != nil {
		return nil
	}
	users := make([]UserProfile, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id, err := strconv.Atoi(entry.Name())
		if err != nil || id < 0 {
			continue
		}
		kind := "profile"
		name := "user-" + entry.Name()
		if id == 0 {
			kind = "main"
			name = "owner"
		} else if id >= 900 {
			kind = "clone"
			name = "clone-" + entry.Name()
		}
		users = append(users, UserProfile{ID: id, Name: name, Kind: kind, Alive: true})
	}
	sort.Slice(users, func(i, j int) bool { return users[i].ID < users[j].ID })
	return users
}

func (r Resolver) listPackagesForUser(ctx context.Context, userID int, base map[string]int) ([]AppInstance, error) {
	raw, err := r.Run(ctx, "cmd", "package", "list", "packages", "--user", strconv.Itoa(userID), "-U", "-f")
	if err != nil {
		raw, err = r.Run(ctx, "pm", "list", "packages", "--user", strconv.Itoa(userID), "-U", "-f")
	}
	if err != nil {
		return nil, err
	}
	items := parsePackageListOutput(raw, userID)
	if len(items) > 0 {
		fillMissingUIDsFromBase(items, base, userID)
		return items, nil
	}
	return fallbackInstancesForUser(base, userID), nil
}

func parsePackageListOutput(raw []byte, userID int) []AppInstance {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	out := make([]AppInstance, 0)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "package:") {
			continue
		}
		fields := strings.Fields(line)
		pkg, sourceDir := parsePackageSpec(fields[0])
		uid := 0
		for _, field := range fields[1:] {
			if strings.HasPrefix(field, "uid:") {
				uid, _ = strconv.Atoi(strings.TrimPrefix(field, "uid:"))
				break
			}
		}
		if pkg == "" {
			continue
		}
		uid = normalizeUIDForUser(uid, userID)
		appID := appIDFromUID(uid)
		out = append(out, AppInstance{
			PackageName: pkg,
			UserID:      userID,
			UID:         uid,
			AppID:       appID,
			SourceDir:   sourceDir,
			Source:      "cmd-package",
		})
	}
	return out
}

func parsePackageSpec(value string) (string, string) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "package:")
	if idx := strings.LastIndex(value, "="); idx >= 0 {
		return strings.TrimSpace(value[idx+1:]), strings.TrimSpace(value[:idx])
	}
	return strings.TrimSpace(value), ""
}

func (r Resolver) readPackagesList() (map[string]int, error) {
	file, err := os.Open(r.PackagesListPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	out := make(map[string]int)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		uid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		out[fields[0]] = uid
	}
	return out, scanner.Err()
}

func fallbackInstancesForUser(base map[string]int, userID int) []AppInstance {
	out := make([]AppInstance, 0, len(base))
	for pkg, ownerUID := range base {
		uid, appID, ok := uidForUser(ownerUID, userID)
		if !ok {
			continue
		}
		out = append(out, AppInstance{
			PackageName: pkg,
			UserID:      userID,
			UID:         uid,
			AppID:       appID,
			Source:      "packages.list",
		})
	}
	return out
}

func appIDFromUID(uid int) int {
	if uid <= 0 {
		return 0
	}
	if uid >= androidUserOffset {
		return uid % androidUserOffset
	}
	return uid
}

func fillMissingUIDsFromBase(items []AppInstance, base map[string]int, userID int) {
	if len(base) == 0 {
		return
	}
	for i := range items {
		if items[i].UID > 0 || items[i].PackageName == "" {
			continue
		}
		ownerUID := base[items[i].PackageName]
		if ownerUID <= 0 {
			continue
		}
		uid, appID, ok := uidForUser(ownerUID, userID)
		if !ok {
			continue
		}
		items[i].UID = uid
		items[i].AppID = appID
		if items[i].Source != "" {
			items[i].Source += "+packages.list"
		} else {
			items[i].Source = "packages.list"
		}
	}
}

func normalizeUIDForUser(uid int, userID int) int {
	if uid <= 0 || userID <= 0 || uid >= androidUserOffset {
		return uid
	}
	return userID*androidUserOffset + uid
}

func uidForUser(ownerUID int, userID int) (uid int, appID int, ok bool) {
	if ownerUID <= 0 {
		return 0, 0, false
	}
	appID = appIDFromUID(ownerUID)
	if userID <= 0 {
		return ownerUID, appID, true
	}
	if appID < 10000 {
		return 0, 0, false
	}
	return userID*androidUserOffset + appID, appID, true
}

func addDerivedUserInstances(byPackageUser map[string]AppInstance, rules []config.PackageRule, users []UserProfile, base map[string]int) {
	if len(base) == 0 || len(users) == 0 {
		return
	}
	for _, rule := range rules {
		ownerUID := base[rule.PackageName]
		if ownerUID <= 0 {
			continue
		}
		for _, user := range users {
			if !ruleAllowsUser(rule, user.ID) {
				continue
			}
			key := instanceKey(rule.PackageName, user.ID)
			if _, exists := byPackageUser[key]; exists {
				continue
			}
			uid, appID, ok := uidForUser(ownerUID, user.ID)
			if !ok {
				continue
			}
			byPackageUser[key] = AppInstance{
				PackageName: rule.PackageName,
				UserID:      user.ID,
				UID:         uid,
				AppID:       appID,
				Source:      "packages.list-derived",
			}
		}
	}
}

func appendConfiguredCloneUsers(users []UserProfile, cloneUserIDs []int) []UserProfile {
	for _, uid := range cloneUserIDs {
		if uid > 0 && !containsUser(users, uid) {
			users = append(users, UserProfile{ID: uid, Name: fmt.Sprintf("clone-%d", uid), Kind: "clone", Alive: true})
		}
	}
	return users
}

func appendDataUsers(users []UserProfile, dataUsers []UserProfile) []UserProfile {
	for _, user := range dataUsers {
		if containsUser(users, user.ID) {
			continue
		}
		users = append(users, user)
	}
	sort.Slice(users, func(i, j int) bool { return users[i].ID < users[j].ID })
	return users
}

func ruleAllowsUser(rule config.PackageRule, userID int) bool {
	if len(rule.IncludeUserIDs) > 0 && !containsInt(rule.IncludeUserIDs, userID) {
		return false
	}
	if containsInt(rule.ExcludeUserIDs, userID) {
		return false
	}
	switch rule.Scope {
	case "", "all_instances":
		return true
	case "owner_only":
		return userID == 0
	default:
		return true
	}
}

func uniqueInstances(in []AppInstance) []AppInstance {
	seen := make(map[string]bool)
	out := make([]AppInstance, 0, len(in))
	for _, item := range in {
		if item.UID <= 0 {
			continue
		}
		key := instanceKey(item.PackageName, item.UserID)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
	}
	return out
}

func instanceKey(pkg string, userID int) string {
	return pkg + "@" + strconv.Itoa(userID)
}

func containsUser(users []UserProfile, id int) bool {
	for _, user := range users {
		if user.ID == id {
			return true
		}
	}
	return false
}

func containsInt(values []int, needle int) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func UIDs(instances []AppInstance) []int {
	uids := make([]int, 0, len(instances))
	seen := make(map[int]bool)
	for _, item := range instances {
		if item.UID <= 0 || seen[item.UID] {
			continue
		}
		seen[item.UID] = true
		uids = append(uids, item.UID)
	}
	sort.Ints(uids)
	return uids
}
