## Deploying

Build and push to `edge` tag. mkube's image-policy handles the update — NEVER delete the pod, just push the new image:

```bash
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o ipmiserial .
podman build --no-cache --platform linux/arm64 -t registry.gt.lo:5000/ipmiserial:edge .
podman push --tls-verify=false registry.gt.lo:5000/ipmiserial:edge
```

---

## Deferred: Network CRD + BMH IPMI Filtering

Full plan at: `~/.claude/plans/generic-jingling-acorn.md`

**Problem**: ipmiserial discovers ALL BareMetalHost objects from mkube, including `server1b` (namespace `g10`, BMC 192.168.10.20) which isn't on an IPMI network. Need to filter by network type.

**Solution**: Create a Network CRD in mkube with labels (e.g., `mkube.io/ipmi: "true"`), then filter BMH list/watch by `?labelSelector=mkube.io/ipmi=true` in ipmiserial.

**Scope** (both repos):
- **mkube**: Add `Labels` to `NetworkDef` config, add `Networks` NATS bucket, full CRUD API for Network CRD, seed from config at startup, add labelSelector filtering to BMH list/watch handlers
- **ipmiserial**: Add `NetworkLabel` to `DiscoveryConfig`, pass labelSelector query param in BMH fetch/watch URLs
- **Config**: Add `labels: { mkube.io/ipmi: "true" }` to g11 network in `rose1-config.yaml`
