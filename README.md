# Sonic Siphon

A modern, Docker-based web application for downloading YouTube videos and playlists as MP3 files with adjustable playback speed and an intuitive file management interface.

## Features

- 🎵 **Download YouTube Content**: Single videos or entire playlists
- ⚡ **Speed Adjustment**: Adjust playback speed (0.5x, 1x, 1.5x, 1.75x, 2x) while preserving pitch
- 🖼️ **Thumbnail Embedding**: Automatically embeds video thumbnails as album art
- 🎨 **Modern UI**: Clean, responsive interface built with Tailwind CSS
- 📁 **File Management**: Organize downloads with temp and output directories
- 🔊 **Audio Streaming**: Preview and stream downloaded MP3s directly in the browser
- 🐳 **Docker Ready**: One-command deployment with Docker Compose
- 📊 **Real-time Progress**: Track download progress with live updates

## Prerequisites

- Docker
- Docker Compose

## Quick Start

1. **Start the application**:

   ```bash
   docker-compose up -d
   ```

2. **Access the web interface**:
   Open your browser and navigate to `http://localhost:5000`

3. **Download MP3s**:
   - Paste a YouTube video or playlist URL
   - Optionally select a playback speed
   - Click "Download"
   - Files are saved to `./output` directory

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

### Enable Temp Directory Mounting

To persist temporary files, uncomment the temp volume in `docker-compose.yml`:

```yaml
volumes:
  - ./output:/output
  - ./temp:/temp  # Uncomment this line
```

## How It Works

### Backend

- **Flask**: Web server handling API requests
- **yt-dlp**: Downloads YouTube videos and playlists
- **ffmpeg**: Converts audio and applies speed adjustments using the `atempo` filter

### Frontend

- **Tailwind CSS**: Modern, utility-first CSS framework
- **JavaScript**: Handles UI interactions, real-time updates, and file management

### Processing Pipeline

1. User submits YouTube URL and optional speed setting
2. Backend extracts video/playlist metadata for preview
3. Downloads audio using yt-dlp with embedded thumbnail
4. Converts to MP3 format (192kbps)
5. Applies speed adjustment if specified (preserves pitch)
6. Saves to `/temp` directory initially
7. User can move files to `/output` directory via the UI

## Supported URLs

- Single videos: `https://www.youtube.com/watch?v=VIDEO_ID`
- Playlists: `https://www.youtube.com/playlist?list=PLAYLIST_ID`
- Short URLs: `https://youtu.be/VIDEO_ID`
- Mobile URLs: `https://m.youtube.com/watch?v=VIDEO_ID`

## Speed Adjustment

Speed adjustments use ffmpeg's `atempo` filter, which preserves pitch while changing tempo:

- **0.5x**: Half speed (slower)
- **1x**: Normal speed (no modification)
- **1.5x**: 50% faster
- **1.75x**: 75% faster
- **2x**: Double speed (twice as fast)

For speeds outside the 0.5-2.0 range, multiple `atempo` filters are chained automatically.

## API Endpoints

- `GET /` - Main web interface
- `POST /preview` - Get video/playlist metadata without downloading
- `POST /download` - Start download process
- `GET /status/<download_id>` - Get download status and progress
- `GET /files` - List all MP3 files in temp and output directories
- `GET /stream/<location>/<filename>` - Stream MP3 file for playback
- `GET /thumbnail/<location>/<filename>` - Extract and serve embedded thumbnail
- `POST /move` - Move files from temp to output directory
- `DELETE /delete/<location>/<filename>` - Delete a file

## Development

### Building CSS

If you modify Tailwind CSS source files, rebuild the CSS:

```bash
npm run build:css
```

### Rebuilding the Container

After making changes to the code:

```bash
docker-compose up -d --build
```

### Viewing Logs

```bash
docker-compose logs -f
```

### Stopping the Container

```bash
docker-compose down
```

## CI/CD with GitHub Actions

This repository includes a GitHub Actions workflow that automatically builds and pushes Docker images to Docker Hub on every push to the main/master branch.

### Setup

1. **Create Docker Hub Access Token**:
   - Go to Docker Hub → Account Settings → Security → New Access Token
   - Create a token with read/write permissions
   - Copy the token

2. **Add GitHub Secrets**:
   - Go to your GitHub repository → Settings → Secrets and variables → Actions
   - Add the following secrets:
     - `DOCKERHUB_USERNAME`: Your Docker Hub username (e.g., `fossfrog`)
     - `DOCKERHUB_TOKEN`: Your Docker Hub access token

3. **Push to trigger workflow**:
   - The workflow automatically runs on pushes to `main` or `master` branches
   - You can also manually trigger it from the Actions tab

### Workflow Features

- ✅ Automatic builds on push to main/master
- ✅ Tag support for semantic versioning (e.g., `v1.0.0`)
- ✅ Multi-platform support (via Docker Buildx)
- ✅ Layer caching for faster builds
- ✅ Automatic tagging with branch names, SHA, and semantic versions

### Using the Published Image

Once pushed, you can use the image directly:

```bash
docker pull fossfrog/sonic-siphon:latest
```

Or update `docker-compose.yml`:

```yaml
services:
  sonic-siphon:
    image: fossfrog/sonic-siphon:latest
    container_name: sonic-siphon
    ports:
      - "5000:5000"
    volumes:
      - ./output:/output
    restart: unless-stopped
```

## Troubleshooting

### Downloads Fail

- Verify the YouTube URL is valid and accessible
- Check internet connectivity
- Review container logs: `docker-compose logs`
- Ensure yt-dlp is up to date (rebuild the container)

### Speed Adjustment Not Working

- Verify ffmpeg is installed (included in Docker image)
- Check logs for ffmpeg errors: `docker-compose logs`
- Ensure speed value is between 0.5 and 2.0 (or multiples)

### Can't Access Web Interface

- Ensure port 5000 is not in use by another application
- Check if container is running: `docker-compose ps`
- Verify port mapping in `docker-compose.yml`

### Files Not Appearing

- Check volume mounts in `docker-compose.yml`
- Verify directory permissions on host system
- Review container logs for file system errors

### Thumbnails Not Showing

- Thumbnails are embedded during download
- Some videos may not have thumbnails available
- Check logs for thumbnail extraction errors
