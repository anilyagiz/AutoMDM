# Placeholder brand assets

Temporary AutoMDM mark and wordmark that replace Fleet's logo until the final brand assets exist. They are drawn from
plain shapes (`mark.svg`) and DejaVu Sans Bold; `generate.py` renders them at any size.

Every product file that showed Fleet's logo now holds this placeholder at the original file's pixel size and format:
the web UI logo and favicon, the AutoMDM Desktop tray and app icons (Linux, macOS, Windows), the Chrome extension
icons, the Android agent logo, the agent downloader and the vulnerability dashboard.

`images-permanent/` holds the two images the server's emails load from `https://automdm.com/images/permanent/`. Serve
them there (the other images the emails reference, `slack-logo-41x40@2x.png`, are third-party logos).

A designer should deliver: a square mark (SVG), a horizontal wordmark for light and dark backgrounds (SVG), app icons
at 16 to 1024 px, `.ico` and `.icns` files, a single-color macOS menu bar template icon, and the email header image at
324x184 px.
