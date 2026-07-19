# OpenWrt 源码包哈希值计算

自 OpenWrt 25.12 版本开始，使用 `PKG_SOURCE_PROTO:=git` 的包源码在非 `PKG_MIRROR_HASH:=skip` 条件下有严格的完整性要求。

此工具根据 OpenWrt [download.mk](https://github.com/openwrt/openwrt/blob/main/include/download.mk) 相同的逻辑获取包源码并计算 HASH 值，方便在 OpenWrt 源码外的环境计算正确的 HASH。

由于 zstd 版本不一致可能会导致结果有出入，源码包含了静态构建的 zstd 工具（x86_64）。

# 使用

- -V 详细输出
- -s 保存源码包

```
Usage: pkghash [-V] [-s] <git_url> <commit_hash> <pkg_name> <pkg_version>
```

```
pkghash -V https://github.com/daeuniverse/dae.git 5a51cc747ef9e17185d438dc54ebf32c681984db dae 2026.06.14
=> Cloning repository...
=> Commit timestamp: @1781406379
=> Generating formal git archive...
=> Updating submodules...
=> Packing deterministic tarball...
b379ccdb53b439fb662aa8be436ba26c90db8bc4dc042883b1724f324dd3c5ef
```
