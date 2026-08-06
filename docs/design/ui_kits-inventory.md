<!-- docs/design/ui_kits-inventory.md -->
# Design intake inventory

Source of record: Claude Design project "Aether LIVE Design System"
(projectId 65d232d0-9a49-4676-aa1c-29446ef9f179), pulled 2026-08-06 via the
DesignSync get_file method. Files under docs/design/ mirror the source paths
byte for byte. They are third party verbatim artifacts: no path comments were
added and no characters were changed.

| File (repo path under docs/design/) | Bytes | SHA-256 |
| --- | --- | --- |
| colors_and_type.css | 8719 | 8aaebe896595d6c0711b5dd707e25b7f6b8a0a158881356f00d6e3395609f5cd |
| tokens.json | 6736 | 9ee0cb5210a8bd617ff106f049979bc3c8a269e61d624870772909edcf5e1949 |
| ui_kits/aether-edit/Icons.jsx | 1764 | c3e34c525d45964a996fd1ba36b4220f6e674d3e71c9e604d9bd0323d70bea35 |
| ui_kits/aether-live/FileTranscoder.jsx | 35432 | bb67b874599656b85f4a19fafeb30c5c60cb420dba5b690f51b1346f6534ad28 |
| ui_kits/aether-live/File_transcoder.html | 2911 | 3945aa1852ac19f3d3c466e33da2405e1d23147473183be67d66884cb33ecb7a |
| ui_kits/aether-live/HANDOFF.md | 11482 | 658355c73aa1eb06fc5eb8d2296806b0688d0a0629c38a7918af63dc48723778 |
| ui_kits/aether-live/Live_transcoder.html | 2795 | 9ce74d6761249db70261301ade09ef5bcbecf20024e798c254b41c820c6cf75c |
| ui_kits/aether-live/Parts.jsx | 11628 | d6876b5270449a95765d51c982ea9803a41684a04cb26e62b17156927421c24e |
| ui_kits/aether-live/Transcoder.jsx | 22851 | b9286deae9bba1edd3075f6a98d5bb9c475b35490d683a3bd94542b96ef758a8 |
| ui_kits/aether-live/Uploader.jsx | 11160 | 6808c3d435da04d3cb3d84c9f68731b299795def262fc514f63ae7381f55aba1 |
| ui_kits/aether-live/image-slot.js | 65350 | fff26d081c8d9d60870f86c7539a5d179b9cdab15e67f2b205508a068e7c7ff6 |

Total: 11 files, 181128 bytes.

## Fidelity checks performed

1. Every fetch returned truncated=false and isBase64=false; content was written
   with LF endings and no BOM, exactly as returned.
2. docs/design/ui_kits/aether-live/HANDOFF.md was compared against the relayed
   handoff at /tmp/design_handoff_transcoders.md with that file's provenance
   comment block (everything above its first "---" separator) stripped. Result:
   byte identical below the provenance block.
