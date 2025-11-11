# YouTube to MP3 Downloader (Docker)

A Docker-based web application for downloading YouTube videos and playlists as MP3 files with adjustable playback speed.

## Features

- 🎵 Download single YouTube videos or entire playlists
- ⚡ Adjustable playback speed (0.5x, 1x, 1.5x, 1.75x, 2x)
- 🌐 Clean and modern web interface
- 🐳 Easy Docker deployment with volume mounting
- 📁 Files automatically saved to mounted `/output` directory

## Prerequisites

- Docker
- Docker Compose

## Quick Start

1. **Build and run the container:**

```bash
docker-compose up -d
```

2. **Access the web interface:**

Open your browser and navigate to: `http://localhost:5000`

3. **Download MP3s:**
   - Paste a YouTube video or playlist URL
   - Select your desired playback speed
   - Click "Download"
   - Files will be saved to `./output` directory

## Configuration

### Change Port

Edit `docker-compose.yml` to change the exposed port:

```yaml
ports:
  - "8080:5000"  # Change 8080 to your desired port
```

### Change Output Directory

Edit `docker-compose.yml` to change the output directory:

```yaml
volumes:
  - /your/custom/path:/output  # Change to your desired path
```

## Project Structure

```
yt2mp3docker/
├── app.py                 # Flask backend application
├── templates/
│   └── index.html        # Web interface
├── requirements.txt      # Python dependencies
├── Dockerfile           # Docker image configuration
├── docker-compose.yml   # Docker Compose configuration
├── output/              # Downloaded MP3 files (created on first run)
└── README.md           # This file
```

## How It Works

1. **Backend**: Python Flask server with yt-dlp for downloading and ffmpeg for audio processing
2. **Frontend**: Clean HTML/CSS/JavaScript interface for easy interaction
3. **Processing**: 
   - Downloads video/playlist using yt-dlp
   - Converts to MP3 format
   - Applies speed adjustment if selected (using ffmpeg's atempo filter)
   - Saves to `/output` directory (mounted to `./output` on host)

## Supported URLs

- Single videos: `https://www.youtube.com/watch?v=VIDEO_ID`
- Playlists: `https://www.youtube.com/playlist?list=PLAYLIST_ID`
- Short URLs: `https://youtu.be/VIDEO_ID`

## Speed Adjustment Notes

- **0.5x**: Half speed (slower)
- **1x**: Normal speed (no modification)
- **1.5x**: 50% faster
- **1.75x**: 75% faster
- **2x**: Double speed (twice as fast)

Speed adjustments use ffmpeg's `atempo` filter which preserves pitch while changing tempo.

## Stopping the Container

```bash
docker-compose down
```

## Rebuilding After Changes

```bash
docker-compose up -d --build
```

## Logs

View container logs:

```bash
docker-compose logs -f
```

## Troubleshooting

**Issue**: Downloads fail
- Check if the YouTube URL is valid
- Ensure you have internet connectivity
- Check container logs: `docker-compose logs`

**Issue**: Speed adjustment not working
- Verify ffmpeg is installed in the container (it should be by default)
- Check logs for any ffmpeg errors

**Issue**: Can't access web interface
- Ensure port 5000 is not being used by another application
- Check if the container is running: `docker-compose ps`

## License

This project is for educational purposes. Please respect YouTube's Terms of Service and content creators' rights.

