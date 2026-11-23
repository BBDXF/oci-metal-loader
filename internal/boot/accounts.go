package boot

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"omloader/internal/config"
)

// text file editing helpers: a file is a list of lines (no trailing newline kept).
func splitLines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func joinLines(l []string) string {
	if len(l) == 0 {
		return ""
	}
	return strings.Join(l, "\n") + "\n"
}

func lineName(l string) string { n, _, _ := strings.Cut(l, ":"); return n }

func findLine(l []string, name string) int {
	for i, x := range l {
		if lineName(x) == name {
			return i
		}
	}
	return -1
}

func fieldsOf(l string, n int) []string {
	f := strings.Split(l, ":")
	for len(f) < n {
		f = append(f, "")
	}
	return f
}

// AccountDB is the in-memory image of /etc/{passwd,shadow,group,gshadow}.
type AccountDB struct {
	Passwd, Shadow, Group, Gshadow []string
	HasGshadow                     bool
}

func (d *AccountDB) idsInUse() (uids, gids map[int]bool) {
	uids, gids = map[int]bool{}, map[int]bool{}
	for _, l := range d.Passwd {
		f := fieldsOf(l, 7)
		if n, err := strconv.Atoi(f[2]); err == nil {
			uids[n] = true
		}
		if n, err := strconv.Atoi(f[3]); err == nil {
			gids[n] = true
		}
	}
	for _, l := range d.Group {
		if n, err := strconv.Atoi(fieldsOf(l, 4)[2]); err == nil {
			gids[n] = true
		}
	}
	return
}

// ensureGroup returns the gid of group name, creating it when missing
// (system=true: highest free gid <= 999, else the first free gid >= 1000).
func (d *AccountDB) ensureGroup(name string, gid int, system bool) int {
	if i := findLine(d.Group, name); i >= 0 {
		n, _ := strconv.Atoi(fieldsOf(d.Group[i], 4)[2])
		return n
	}
	_, gids := d.idsInUse()
	switch {
	case gid >= 0 && !gids[gid]:
	case system:
		for gid = 999; gid > 100 && gids[gid]; gid-- {
		}
	default:
		for gid = 1000; gids[gid]; gid++ {
		}
	}
	d.Group = append(d.Group, fmt.Sprintf("%s:x:%d:", name, gid))
	if d.HasGshadow {
		d.Gshadow = append(d.Gshadow, name+":!::")
	}
	return gid
}

func addMember(list []string, name, user string, idx int) []string {
	i := findLine(list, name)
	if i < 0 {
		return list
	}
	f := fieldsOf(list[i], 4)
	var m []string
	if f[idx] != "" {
		m = strings.Split(f[idx], ",")
	}
	for _, x := range m {
		if x == user {
			return list
		}
	}
	f[idx] = strings.Join(append(m, user), ",")
	list[i] = strings.Join(f, ":")
	return list
}

func (d *AccountDB) addToGroup(group, user string) {
	d.Group = addMember(d.Group, group, user, 3)
	if d.HasGshadow {
		d.Gshadow = addMember(d.Gshadow, group, user, 3)
	}
}

func shadowDays() string { return strconv.FormatInt(time.Now().Unix()/86400, 10) }

// SetPassword sets the shadow hash of an existing passwd user (creates the entry when missing).
func (d *AccountDB) SetPassword(name, hash string) {
	i := findLine(d.Shadow, name)
	if i < 0 {
		d.Shadow = append(d.Shadow, fmt.Sprintf("%s:%s:%s:0:99999:7:::", name, hash, shadowDays()))
		return
	}
	f := fieldsOf(d.Shadow[i], 9)
	f[1], f[2] = hash, shadowDays()
	d.Shadow[i] = strings.Join(f, ":")
}

// UserResult describes the resolved account (for home/key handling).
type UserResult struct {
	UID, GID int
	Home     string
	Created  bool
}

// EnsureUser creates or updates a user. Existing users keep uid/gid/home; shell is
// only changed when given; the password only when hash != "".
func (d *AccountDB) EnsureUser(u config.User, hash, defShell string) UserResult {
	var res UserResult
	if i := findLine(d.Passwd, u.Name); i >= 0 {
		f := fieldsOf(d.Passwd[i], 7)
		res.UID, _ = strconv.Atoi(f[2])
		res.GID, _ = strconv.Atoi(f[3])
		res.Home = f[5]
		if u.Shell != "" {
			f[6] = u.Shell
		}
		d.Passwd[i] = strings.Join(f, ":")
	} else {
		uids, gids := d.idsInUse()
		uid := 1000
		for uids[uid] || gids[uid] {
			uid++
		}
		gid := d.ensureGroup(u.Name, uid, false)
		sh := u.Shell
		if sh == "" {
			sh = defShell
		}
		res = UserResult{UID: uid, GID: gid, Home: "/home/" + u.Name, Created: true}
		d.Passwd = append(d.Passwd, fmt.Sprintf("%s:x:%d:%d:%s:%s:%s", u.Name, uid, gid, u.Name, res.Home, sh))
		if findLine(d.Shadow, u.Name) < 0 {
			d.Shadow = append(d.Shadow, fmt.Sprintf("%s:!:%s:0:99999:7:::", u.Name, shadowDays()))
		}
	}
	if hash != "" {
		d.SetPassword(u.Name, hash)
	}
	for _, g := range u.Groups {
		d.ensureGroup(g, -1, true)
		d.addToGroup(g, u.Name)
	}
	return res
}

func readGuest(root, p string) (string, bool) {
	hp, err := ResolveInRoot(root, p)
	if err != nil {
		return "", false
	}
	b, err := os.ReadFile(hp)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// guestChown chowns with numeric ids; unprivileged failures (unit tests) are ignored.
func guestChown(root, p string, uid, gid int) error {
	hp, err := walkInRoot(root, p, false)
	if err != nil {
		return err
	}
	if err := os.Lchown(hp, uid, gid); err != nil && os.Geteuid() == 0 {
		return err
	}
	return nil
}

// guestWriteKeep writes a file keeping the mode/owner of an existing one (default def).
func guestWriteKeep(root, p, data string, def os.FileMode, gid int) error {
	mode, existed := def, false
	if hp, err := ResolveInRoot(root, p); err == nil {
		if fi, err := os.Stat(hp); err == nil {
			mode, existed = fi.Mode().Perm(), true
		}
	}
	if err := guestWrite(root, p, []byte(data), mode); err != nil {
		return err
	}
	if !existed && gid >= 0 {
		return guestChown(root, p, 0, gid)
	}
	return nil
}

// LoadAccounts reads the account database of a guest root.
func LoadAccounts(root string) *AccountDB {
	d := &AccountDB{}
	s, _ := readGuest(root, "/etc/passwd")
	d.Passwd = splitLines(s)
	s, _ = readGuest(root, "/etc/shadow")
	d.Shadow = splitLines(s)
	s, _ = readGuest(root, "/etc/group")
	d.Group = splitLines(s)
	s, d.HasGshadow = readGuest(root, "/etc/gshadow")
	d.Gshadow = splitLines(s)
	return d
}

// Save writes the database back (existing modes/owners are preserved).
func (d *AccountDB) Save(root string) error {
	gid := -1
	if i := findLine(d.Group, "shadow"); i >= 0 {
		gid, _ = strconv.Atoi(fieldsOf(d.Group[i], 4)[2])
	}
	if err := guestWriteKeep(root, "/etc/passwd", joinLines(d.Passwd), 0o644, -1); err != nil {
		return err
	}
	if err := guestWriteKeep(root, "/etc/group", joinLines(d.Group), 0o644, -1); err != nil {
		return err
	}
	if err := guestWriteKeep(root, "/etc/shadow", joinLines(d.Shadow), 0o640, gid); err != nil {
		return err
	}
	if d.HasGshadow {
		return guestWriteKeep(root, "/etc/gshadow", joinLines(d.Gshadow), 0o640, gid)
	}
	return nil
}

// ResolvePassword returns the shadow hash for a (hash, plain) pair; plain passwords are hashed
// with SHA-512 crypt and a warning is printed (never the password itself).
func ResolvePassword(what, hash, plain string, warn io.Writer) (string, error) {
	if hash != "" {
		return hash, nil
	}
	if plain == "" {
		return "", nil
	}
	fmt.Fprintf(warn, "warning: %s: plain-text password in the config; use password_hash (e.g. `openssl passwd -6`) instead\n", what)
	return HashPassword(plain)
}

func guestMkdirOwned(root, p string, mode os.FileMode, uid, gid int) error {
	hp, err := walkInRoot(root, p, true)
	if err != nil {
		return err
	}
	if err := os.Chmod(hp, mode); err != nil {
		return err
	}
	return guestChown(root, p, uid, gid)
}

func copySkel(root, home string, uid, gid int) {
	sk, err := ResolveInRoot(root, "/etc/skel")
	if err != nil {
		return
	}
	ents, err := os.ReadDir(sk)
	if err != nil {
		return
	}
	for _, e := range ents {
		src := filepath.Join(sk, e.Name())
		if fi, err := os.Lstat(src); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		dst := path.Join(home, e.Name())
		if guestExists(root, dst) {
			continue
		}
		if b, err := os.ReadFile(src); err == nil {
			if guestWrite(root, dst, b, 0o644) == nil {
				guestChown(root, dst, uid, gid)
			}
		}
	}
}

// ApplyAccounts creates/updates users, passwords, sudoers drop-ins and ssh keys in root.
func ApplyAccounts(root string, rt config.Runtime, warn io.Writer) error {
	if warn == nil {
		warn = io.Discard
	}
	if len(rt.Users) == 0 && rt.RootPassword == "" && rt.RootPasswordHash == "" {
		return nil
	}
	pw := warn
	if rt.FromDefaults {
		pw = io.Discard // the built-in default password is public by design; no per-boot warning
	}
	db := LoadAccounts(root)
	defShell := "/bin/sh"
	if guestExists(root, "/bin/bash") {
		defShell = "/bin/bash"
	}
	if h, err := ResolvePassword("runtime.root_password", rt.RootPasswordHash, rt.RootPassword, pw); err != nil {
		return err
	} else if h != "" {
		if findLine(db.Passwd, "root") < 0 {
			db.Passwd = append(db.Passwd, "root:x:0:0:root:/root:"+defShell)
		}
		db.SetPassword("root", h)
	}
	type post struct {
		u config.User
		r UserResult
		h string
	}
	var posts []post
	for _, u := range rt.Users {
		h, err := ResolvePassword("runtime.users["+u.Name+"].password", u.PasswordHash, u.Password, pw)
		if err != nil {
			return err
		}
		posts = append(posts, post{u, db.EnsureUser(u, h, defShell), h})
	}
	if err := db.Save(root); err != nil {
		return fmt.Errorf("write account database: %w", err)
	}
	for _, p := range posts {
		u, r := p.u, p.r
		if r.Created {
			if err := guestMkdirOwned(root, r.Home, 0o755, r.UID, r.GID); err != nil {
				return err
			}
			copySkel(root, r.Home, r.UID, r.GID)
		} else if !guestDirExists(root, r.Home) {
			if err := guestMkdirOwned(root, r.Home, 0o755, r.UID, r.GID); err != nil {
				return err
			}
		}
		if len(u.SSHKeys) > 0 {
			if err := guestMkdirOwned(root, r.Home+"/.ssh", 0o700, r.UID, r.GID); err != nil {
				return err
			}
			ak := r.Home + "/.ssh/authorized_keys"
			if err := guestWrite(root, ak, []byte(strings.Join(u.SSHKeys, "\n")+"\n"), 0o600); err != nil {
				return err
			}
			if err := guestChown(root, ak, r.UID, r.GID); err != nil {
				return err
			}
		}
		sf := "/etc/sudoers.d/90-omloader-" + u.Name
		if u.Sudo {
			rule := u.Name + " ALL=(ALL:ALL) NOPASSWD: ALL\n"
			if p.h != "" || u.Password != "" {
				rule = u.Name + " ALL=(ALL:ALL) ALL\n"
			}
			if !guestExists(root, "/usr/bin/sudo") && !guestExists(root, "/bin/sudo") {
				fmt.Fprintf(warn, "warning: user %s: sudo: true but sudo is not installed in the image (drop-in written anyway)\n", u.Name)
			}
			if err := guestWrite(root, sf, []byte(rule), 0o440); err != nil {
				return err
			}
		} else {
			guestRemove(root, sf)
		}
	}
	return nil
}

func guestDirExists(root, p string) bool {
	hp, err := ResolveInRoot(root, p)
	if err != nil {
		return false
	}
	fi, err := os.Stat(hp)
	return err == nil && fi.IsDir()
}

// guestRemove deletes a file (not following a final symlink); missing is fine.
func guestRemove(root, rel string) {
	dir, err := walkInRoot(root, path.Dir(rel), false)
	if err != nil {
		return
	}
	os.Remove(filepath.Join(dir, path.Base(rel)))
}
