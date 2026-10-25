Locally embedded terminal assets; no CDN or package installation at runtime.

- `@xterm/xterm` 6.0.0: `xterm.js`, `xterm.css`, `xterm.LICENSE` (MIT).
- `@xterm/addon-fit` 0.11.0: `addon-fit.js`, `addon-fit.LICENSE` (MIT).

Downloaded from the official npm registry with `npm pack`. Original tarball
SHA-256 checksums:

```
908e66e04af6c8dc6b00dd3b54de088e2e81e5ed866284fd6c2fb3c2d1c7a3f6  xterm-xterm-6.0.0.tgz
26003b4517a132b64e4ff228fd88a5fda3fff5e606c76093f6dcff772e9ecec0  xterm-addon-fit-0.11.0.tgz
```

Review upstream changes and licenses before updating. xterm creates dynamic
styles, so only the dedicated console document permits inline CSS; scripts
remain local and inline JavaScript is forbidden. No clipboard, link, attach,
WebSocket or other addon is enabled.
