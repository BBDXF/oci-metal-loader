# Sources and mirrors (`sources.yaml`)

`omloader pull`, `omloader images list` and the TUI *Pull* tab read their catalogs from **sources**. Every source can be reached
through the official server or through one of several **mirrors**. All of it is defined in ONE file, `sources.yaml`; there are
no server URLs in the Go code except the embedded copy of that file (`configs/sources.yaml`).

## Where the file lives (later wins)

1. built in (embedded in the binary, `configs/sources.yaml`)
2. `/etc/omloader/sources.yaml` (installed in the live ISO by `build/mk-iso-inner.sh`)
3. `<ISO host partition>/omloader/sources.yaml` (the partition the ISO file was found on, Ventoy)
4. `/mnt/snap/omloader/sources.yaml` (OMLOADER partition)
5. `/mnt/images/omloader/sources.yaml` or `/mnt/images/sources.yaml` (**VTOYDATA** — the place for your own copy)
6. `$OML_SOURCES`, then `sources.yaml` / `omloader/sources.yaml` next to the file given with `-c`
7. finally `sources:` inside `omloader.yaml` / `boot.yaml` (the same keys; this wins over every sources file)

Layers are deep-merged field by field: you only write what changes. A source or mirror cannot be deleted by a later layer, it
is switched off with `enabled: false`. Lists (`rewrite`, `mirrors`) are replaced, not appended. Only the keys `sources` and
`default_source` are allowed in a sources file; anything else is an error naming file and line. Check with
`omloader config validate -c omloader.yaml` (errors such as `sources.lxc-images.servers.nju.url: required`),
`omloader config show --resolved` shows which layer/line every value comes from. JSON schema: `schema/sources.schema.json`.

## Format

```yaml
default_source: lxc-images            # used when neither --source nor a spec prefix is given
sources:
  NAME:                               # [a-z0-9][a-z0-9_.-]*
    title: free text
    type: simplestreams               # simplestreams | http | oci-registry | local
    url: https://images.example.org   # the official server (oci-registry: a registry host)
    enabled: true                     # false = hide/disable (also for a built-in one)
    mirror: official                  # official | auto | a server id below
    mirrors: []                       # extra plain URLs after the official one (legacy, kept for omloader.yaml users)
    proxy: ""                         # per source: http://.. | socks5://.. | direct
    rewrite: []                       # [{from: URL-prefix, to: URL-prefix}] applied to every URL of the source
    servers:                          # named mirrors
      ID:
        title: free text
        url: https://mirror.example/lxc-images   # base URL (oci-registry: host or https://host)
        path: ""                                  # optional path appended to url
        region: cn                                # used by mirror_policy.region_preset
        enabled: true                             # false removes a built-in mirror
        rewrite:                                  # prefix rewrites of the RELATIVE path (index, catalog, blobs), this server only
          - {from: "streams/v1/", to: "meta/simplestreams/v1/"}
```

Selection (`mirror`, `--mirror`):

| value | meaning |
|---|---|
| `official` (default) | the source `url`, then `mirrors:`; with `mirror_policy.region_preset: cn` also the `region: cn` servers as fallbacks |
| `auto` | the official server, `mirrors:` and every enabled server are probed (HEAD of the index); they are tried fastest first, failing ones last. If one fails during a download the next is used |
| `<id>` | that server first, the official server and the rest as fallback (`mirror_policy.strategy: mirror-only` = only that server) |

Mirrors only supply bytes: every image file is sha256-checked against the catalog. The catalog itself is not signature-verified
(a warning says when it came from a mirror).

Requests send `User-Agent: omloader/1` because USTC refuses Go's default one.

## Built-in sources and mirrors

Verified on 2026-11-23 from a real machine (index + catalog downloaded by `omloader images list --mirror X`; HTTP Range request
on an image file; `omloader source test --mirror auto`).

| source | official | mirrors (id) | notes |
|---|---|---|---|
| `lxc-images` (default) | images.linuxcontainers.org | `tuna` mirrors.tuna.tsinghua.edu.cn/lxc-images, `nju` mirror.nju.edu.cn/lxc-images, `cernet` mirrors.cernet.edu.cn/lxc-images | debian, ubuntu (+ desktop VM variants), mint (containers), arch, alpine, fedora, ... Metadata is in `meta/simplestreams/v1/` on TUNA/NJU: the `rewrite` of each server maps `streams/v1/` there (TUNA redirects, NJU answers 404 otherwise). **USTC has no lxc-images** (404). BFSU is an alias of TUNA. |
| `lxd-images` | images.lxd.canonical.com | – | Canonical's LXD server, same layout, subset of the above |
| `ubuntu-releases` | cloud-images.ubuntu.com/releases | `nju` mirror.nju.edu.cn/ubuntu-cloud-images/releases | Ubuntu cloud/server images, no desktop. TUNA/USTC/BFSU/CERNET have no `releases/streams` index (404) |
| `ubuntu-daily` | cloud-images.ubuntu.com/daily | `tuna`, `ustc`, `nju`, `cernet` (`…/ubuntu-cloud-images/daily`) | |
| `dockerhub` (experimental) | registry-1.docker.io | `1ms` docker.1ms.run, `daocloud` docker.m.daocloud.io, `xuanyuan` docker.xuanyuan.me, `vvvv` proxy.vvvv.ee, `jiaxin` docker.jiaxin.site, `hubfast` free.hubfast.cn | `/v2/` answers 401 = alive. Not listed because they failed: docker.nju.edu.cn (403), hub.rat.dev (redirect), docker.mirrors.ustc.edu.cn, mirror.ccs.tencentyun.com, dockerproxy.net / dockerproxy.link / registry.cyou (flaky), 163/baidu/sjtug hosts (unreachable) |

`mirrors.cernet.edu.cn` ([help.mirrors.cernet.edu.cn](https://help.mirrors.cernet.edu.cn/)) is CERNET's unified entry: it redirects to the
nearest university mirror that carries the repository (observed: NJU for lxc-images, USTC for ubuntu-cloud-images). Because the
redirect target differs, `cernet` uses the layout that works on both TUNA and NJU.

Mirrors can answer `429` when polled hard (NJU did during testing) — `auto`/fallback then moves on.

## CLI

```
omloader source list [NAME]                       sources, servers (* = selected), default
omloader source test [NAME] [--mirror auto]       latency of every server, "fastest:" line (registries: GET /v2/)
omloader images list --source ubuntu-releases --mirror nju [--variant desktop] [--type vm] [keywords]
omloader pull lxc:ubuntu/noble/desktop --vm --mirror tuna        # = --variant desktop; spec prefix lxc = default source
omloader pull ubuntu-releases:ubuntu/noble [--vm] --mirror auto  # prefix = source name
omloader source ref alpine:3 --mirror auto        # Docker Hub reference for a proxy: docker.m.daocloud.io/library/alpine:3
```

TUI: *Pull* tab, `m` cycles the mirror of the selected source (official, auto, ids), `t` tests it (latency per server),
Enter loads the catalog through it; the pull uses the same mirror. See docs/tui.md.

### Variants

`--variant` (or the third part of the spec) selects the variant: `default`, `cloud`, `desktop`, ... Ubuntu cloud images have only
`default`. `omloader images list ubuntu desktop` shows them (VM only: jammy, noble, resolute, stonking when checked).

## Ubuntu cloud images

Catalog items used: `lxd.tar.xz` (metadata) + `squashfs` (container) or `disk1.img` (VM, qcow2). A container pull is the same path
as linuxcontainers (squashfs rootfs). A VM pull imports `disk1.img` like the linuxcontainers `disk-kvm.img`
(kernel/initrd/root partition detection by the same code). The catalog listing and mirror selection for these sources was tested against the
real servers; a full multi-GB pull of an Ubuntu cloud image was NOT run in the tests.

## Deepin, Linux Mint, others

* **Linux Mint**: only as *containers* (`mint/vanessa|vera|victoria|virginia|wilma`, `default`/`cloud`) in `lxc-images`; no VM images.
* **Deepin**: no LXD/simplestreams catalog exists anywhere we could find (linuxcontainers, Canonical): there is nothing to add.
  Build or convert an image yourself and `omloader import lxd-vm PATH --name deepin` (disk.qcow2) or `omloader import lxd ...`.
* Anything with a simplestreams server of its own can be added as a source (below); Debian desktop images do not exist as catalog images.

## Add your own source or mirror

`<VTOYDATA>/omloader/sources.yaml`:

```yaml
default_source: lxc-images
sources:
  lxc-images:
    mirror: auto                      # fastest working mirror, fall back to the others
    servers:
      tuna: {enabled: false}          # remove a built-in mirror
      sjtu:                           # add one (a different base URL + path, own metadata location)
        title: Company mirror
        url: https://mirror.corp.example
        path: /images/lxc
        rewrite: [{from: "streams/v1/", to: "meta/simplestreams/v1/"}]
  lxd-images: {enabled: false}        # hide a built-in source
  corp-images:                        # a private simplestreams server (index at streams/v1/index.json)
    title: Corp image server
    type: simplestreams
    url: https://images.corp.example
    proxy: direct
  dockerhub:
    mirror: auto
    servers:
      corp-proxy: {url: registry.corp.example}
```

## Docker Hub (experimental)

OCI images stay experimental (`experimental.oci_images`) and omloader has **no registry client**: `dockerhub` and its proxies are only
used by `omloader source test dockerhub [--mirror auto]` (reachability/latency) and `omloader source ref IMAGE` (prints the image
reference rewritten for the chosen proxy). Fetch the image on a machine with skopeo and import it:

```
skopeo copy docker://$(omloader source ref nginx:1 --mirror auto) oci-archive:nginx.tar:nginx
omloader import oci-archive nginx.tar --name nginx --out IMAGES_ROOT --experimental-oci
```
