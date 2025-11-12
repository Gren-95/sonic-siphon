# Sonic Siphon

Sonic Siphon is a sleek, user-friendly web app for downloading YouTube videos and playlists as high-quality MP3 files. Enjoy adjustable playback speed *before* downloading, robust file management, and a modern, responsive interface—all powered by Docker for painless setup and deployment.

---

## 🎨 Demo

Try out the interface: **[Live Demo](https://fossfrog.github.io/sonic-siphon/)**

*Note: The demo shows the UI with mock data. Full functionality requires running the application locally.*

---

## 🚀 Quick Start (with Docker)

**1. Clone and launch Sonic Siphon:**

```bash
git clone https://github.com/fossfrog/sonic-siphon.git
cd sonic-siphon
docker-compose up -d
```

**2. Open the app in your browser:**

Go to [http://localhost:5000](http://localhost:5000)

**3. Download MP3s from YouTube:**
- 📝 Paste a YouTube video or playlist URL in the provided field (supports `youtube.com` and `youtu.be` links)
- 🎚️ Choose an optional playback speed—your MP3 will be encoded at this speed, with pitch preserved
- ⬇️ Click **Download** and watch the progress
- 📂 Find your processed files in the `./temp` directory on your machine
- 📂 Move the processed files to the `./output` directory once you are satisfied with the end product.

---

## ⚙️ Configuration

You can change where files are saved by editing the output path in your `docker-compose.yml` file:

```yaml
services:
  sonic-siphon:
    image: fossfrog/sonic-siphon:latest
    container_name: sonic-siphon
    ports:
      - "5000:5000"
    volumes:
      - /your/local/music:/output    # ← Change this path to choose your output folder
      # - ./temp:/temp               # (Optional) Enable to persist temp files between restarts

    restart: unless-stopped
```
