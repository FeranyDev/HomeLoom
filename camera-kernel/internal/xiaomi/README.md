# Xiaomi MISS (HomeLoom)

HomeLoom only enables the pre-authorized Xiaomi MISS path.

- Account login, cloud device listing, and long-lived Xiaomi credentials stay in
  HomeLoom Core / Camera Provider.
- Camera Kernel receives a short-lived MISS source constructed by Media Worker.
- Legacy Xiaomi producers and cloud login modules are not compiled into this tree.

Supported runtime shape:

```text
xiaomi:<preauthorized-miss-source>
```

Do not reintroduce `xiaomi/legacy` or account-login helpers without updating the
Camera Provider contract and implementation plan.
