package simplestreams

import (
	"strings"
	"testing"
)

const idx = `{"format":"index:1.0","index":{"images":{"datatype":"image-downloads","path":"streams/v1/images.json","format":"products:1.0","products":["ubuntu:noble:amd64:default"]}}}`
const cat = `{"format":"products:1.0","products":{
"ubuntu:noble:amd64:default":{"aliases":"ubuntu/noble,ubuntu/noble/default","arch":"amd64","os":"Ubuntu","release":"noble","variant":"default","versions":{
 "20240101_07:42":{"items":{"lxd.tar.xz":{"ftype":"lxd.tar.xz","sha256":"a1","size":1,"path":"images/old/lxd.tar.xz"},"root.squashfs":{"ftype":"squashfs","sha256":"a2","size":2,"path":"images/old/root.squashfs"}}},
 "20240201_07:42":{"items":{"lxd.tar.xz":{"ftype":"lxd.tar.xz","sha256":"b1","size":1,"path":"images/new/lxd.tar.xz"},"root.squashfs":{"ftype":"squashfs","sha256":"b2","size":2,"path":"images/new/root.squashfs"},"disk.qcow2":{"ftype":"disk-kvm.img","sha256":"b3","size":3,"path":"images/new/disk.qcow2"}}}}},
"debian:bookworm:arm64:default":{"arch":"arm64","os":"Debian","release":"bookworm","variant":"default","versions":{"1":{"items":{"lxd.tar.xz":{"ftype":"lxd.tar.xz","sha256":"c1","path":"x"},"root.squashfs":{"ftype":"squashfs","sha256":"c2","path":"y"}}}}}}}`

func TestFind(t *testing.T) {
	ix, err := ParseIndex(strings.NewReader(idx))
	if err != nil || len(ix.ImagePaths()) != 1 || ix.ImagePaths()[0] != "streams/v1/images.json" {
		t.Fatal(err, ix)
	}
	c, err := ParseCatalog(strings.NewReader(cat))
	if err != nil {
		t.Fatal(err)
	}
	all := c.Find(Query{Arch: "amd64"})
	if len(all) != 2 || all[0].VM || !all[1].VM {
		t.Fatalf("%+v", all)
	}
	ct, err := c.Latest(Query{OS: "ubuntu", Release: "noble", Variant: "default", Arch: "amd64", Kind: "container"})
	if err != nil || ct.Version != "20240201_07:42" || ct.Root.SHA256 != "b2" || ct.Meta.Path != "images/new/lxd.tar.xz" {
		t.Fatalf("%+v %v", ct, err)
	}
	vm, err := c.Latest(Query{OS: "ubuntu", Release: "noble", Arch: "amd64", Kind: "vm"})
	if err != nil || !vm.VM || vm.Root.Path != "images/new/disk.qcow2" {
		t.Fatalf("%+v %v", vm, err)
	}
	if _, err := c.Latest(Query{OS: "ubuntu", Release: "noble", Arch: "amd64"}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatal("want ambiguous", err)
	}
	if _, err := c.Latest(Query{OS: "debian", Arch: "amd64"}); err == nil {
		t.Fatal("arm64 must not match amd64")
	}
	if len(c.Find(Query{Release: "ubuntu/noble", Arch: "amd64", Kind: "vm"})) != 1 {
		t.Fatal("alias match")
	}
}

// cloud-images.ubuntu.com style catalog: no "variant", items squashfs / disk1.img / lxd.tar.xz (+ many others we ignore).
func TestUbuntuCloudImagesCatalog(t *testing.T) {
	const cat = `{"products":{"com.ubuntu.cloud:server:24.04:amd64":{"aliases":"24.04,n,noble","arch":"amd64","os":"ubuntu","release":"noble","versions":{
	"20260101":{"items":{"lxd.tar.xz":{"ftype":"lxd.tar.xz","sha256":"a","size":1,"path":"server/releases/noble/release-20260101/x-lxd.tar.xz"},
	 "squashfs":{"ftype":"squashfs","sha256":"b","size":2,"path":"server/releases/noble/release-20260101/x.squashfs"},
	 "disk1.img":{"ftype":"disk1.img","sha256":"c","size":3,"path":"server/releases/noble/release-20260101/x.img"},
	 "ova":{"ftype":"ova","sha256":"d","size":4,"path":"p/x.ova"},"root.tar.xz":{"ftype":"root.tar.xz","sha256":"e","size":5,"path":"p/x-root.tar.xz"}}}}}}}`
	c, err := ParseCatalog(strings.NewReader(cat))
	if err != nil {
		t.Fatal(err)
	}
	imgs := c.Find(Query{OS: "ubuntu", Release: "noble", Variant: "default", Arch: "amd64"})
	if len(imgs) != 2 || imgs[0].VM || !imgs[1].VM || imgs[0].Variant != "default" ||
		imgs[0].Root.Path != "server/releases/noble/release-20260101/x.squashfs" || imgs[1].Root.Path != "server/releases/noble/release-20260101/x.img" {
		t.Fatalf("%+v", imgs)
	}
	if len(c.Find(Query{Release: "24.04", Variant: "desktop"})) != 0 {
		t.Fatal("ubuntu cloud images have no desktop variant")
	}
}
