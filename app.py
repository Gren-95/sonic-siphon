from flask import Flask, render_template, request, jsonify, send_from_directory
import yt_dlp
import os
import subprocess
from pathlib import Path
import threading
import uuid
import shutil
import logging

app = Flask(__name__)

# Directories
TEMP_DIR = '/temp'
OUTPUT_DIR = '/output'
Path(TEMP_DIR).mkdir(parents=True, exist_ok=True)
Path(OUTPUT_DIR).mkdir(parents=True, exist_ok=True)

# Store download status
downloads = {}

def change_audio_speed(input_file, output_file, speed):
    """Change audio playback speed using ffmpeg
    Note: atempo filter only accepts values between 0.5 and 2.0
    For other speeds, we need to chain multiple atempo filters
    Preserves all metadata including embedded cover art
    """
    try:
        # Build atempo filter chain
        # atempo only works between 0.5 and 2.0, so we chain if needed
        atempo_filters = []
        remaining_speed = speed
        
        while remaining_speed > 2.0:
            atempo_filters.append('atempo=2.0')
            remaining_speed /= 2.0
        
        while remaining_speed < 0.5:
            atempo_filters.append('atempo=0.5')
            remaining_speed /= 0.5
        
        if remaining_speed != 1.0:
            atempo_filters.append(f'atempo={remaining_speed}')
        
        filter_string = ','.join(atempo_filters)
        
        print(f"Applying speed change: {speed}x with filter: {filter_string}")
        
        cmd = [
            'ffmpeg',
            '-i', input_file,
            '-filter:a', filter_string,
            '-map', '0:a',  # Map audio stream
            '-map', '0:v?',  # Map video stream if exists (cover art)
            '-c:v', 'copy',  # Copy video stream (cover art) without re-encoding
            '-id3v2_version', '3',  # Use ID3v2.3 for better compatibility
            '-metadata:s:v', 'title=Album cover',
            '-metadata:s:v', 'comment=Cover (front)',
            '-acodec', 'libmp3lame',  # MP3 codec
            '-b:a', '192k',  # Bitrate
            output_file,
            '-y'  # Overwrite
        ]
        
        result = subprocess.run(cmd, check=True, capture_output=True, text=True)
        print(f"Speed change completed for {os.path.basename(input_file)}")
        return True
    except subprocess.CalledProcessError as e:
        print(f"Error changing speed: {e}")
        print(f"STDOUT: {e.stdout}")
        print(f"STDERR: {e.stderr}")
        return False
    except Exception as e:
        print(f"Unexpected error: {e}")
        return False

def download_task(url, speed, download_id):
    """Background task to download and process audio"""
    import time
    try:
        downloads[download_id]['status'] = 'downloading'
        downloads[download_id]['message'] = 'Downloading...'
        
        # Get list of MP3 files BEFORE download starts
        files_before = set(f for f in os.listdir(TEMP_DIR) if f.endswith('.mp3'))
        print(f"Files before download: {len(files_before)}")
        
        # Configure yt-dlp options
        ydl_opts = {
            'format': 'bestaudio/best',
            'outtmpl': os.path.join(TEMP_DIR, '%(title)s.%(ext)s'),
            'writethumbnail': True,
            'postprocessors': [
                {
                    'key': 'FFmpegExtractAudio',
                    'preferredcodec': 'mp3',
                    'preferredquality': '192',
                },
                {
                    'key': 'EmbedThumbnail',
                    'already_have_thumbnail': False,
                },
                {
                    'key': 'FFmpegMetadata',
                    'add_metadata': True,
                }
            ],
            'progress_hooks': [lambda d: progress_hook(d, download_id)],
            'extractor_args': {'youtube': {'player_client': ['android', 'web']}},
            'nocheckcertificate': True,
            'no_warnings': False,
            'ignoreerrors': False,
        }
        
        with yt_dlp.YoutubeDL(ydl_opts) as ydl:
            info = ydl.extract_info(url, download=True)
            
            # Handle playlist
            if 'entries' in info:
                total_videos = len(info['entries'])
                downloads[download_id]['message'] = f'Downloaded {total_videos} videos from playlist'
                video_titles = [entry['title'] for entry in info['entries'] if entry]
            else:
                video_titles = [info['title']]
                downloads[download_id]['message'] = f'Downloaded: {info["title"]}'
        
        # Small delay to ensure files are fully written
        time.sleep(0.5)
        
        # Get list of MP3 files AFTER download
        files_after = set(f for f in os.listdir(TEMP_DIR) if f.endswith('.mp3'))
        print(f"Files after download: {len(files_after)}")
        
        # Find newly added files
        new_files = list(files_after - files_before)
        print(f"New files detected: {new_files}")
        
        # Apply speed change if not default
        if speed != 1.0:
            downloads[download_id]['status'] = 'processing'
            downloads[download_id]['message'] = f'Applying speed adjustment ({speed}x)...'
            print(f"\n=== Applying speed change: {speed}x ===")
            
            if new_files:
                # Process all newly downloaded files
                for filename in new_files:
                    mp3_file = os.path.join(TEMP_DIR, filename)
                    print(f"Processing: {filename}")
                    temp_file = os.path.join(TEMP_DIR, f'temp_{filename}')
                    
                    if change_audio_speed(mp3_file, temp_file, speed):
                        os.remove(mp3_file)
                        os.rename(temp_file, mp3_file)
                        print(f"✓ Successfully processed: {filename}")
                    else:
                        print(f"✗ Failed to process: {filename}")
                        # Clean up temp file if it exists
                        if os.path.exists(temp_file):
                            os.remove(temp_file)
            else:
                print("⚠ No new files detected to process")
        
        downloads[download_id]['status'] = 'completed'
        downloads[download_id]['message'] = 'Download completed successfully!'
        print(f"=== Download task completed ===\n")
        
    except Exception as e:
        downloads[download_id]['status'] = 'error'
        downloads[download_id]['message'] = f'Error: {str(e)}'

def progress_hook(d, download_id):
    """Update download progress"""
    if d['status'] == 'downloading':
        if 'total_bytes' in d:
            percent = (d['downloaded_bytes'] / d['total_bytes']) * 100
            downloads[download_id]['progress'] = f'{percent:.1f}%'
        elif '_percent_str' in d:
            downloads[download_id]['progress'] = d['_percent_str']

@app.route('/')
def index():
    """Serve the main page"""
    return render_template('index.html')

@app.route('/preview', methods=['POST'])
def preview():
    """Get video info without downloading"""
    try:
        data = request.get_json()
        url = data.get('url')
        
        if not url:
            return jsonify({'error': 'URL is required'}), 400
        
        ydl_opts = {
            'quiet': True,
            'no_warnings': True,
            'extract_flat': False,
            'extractor_args': {'youtube': {'player_client': ['android', 'web']}},
        }
        
        with yt_dlp.YoutubeDL(ydl_opts) as ydl:
            info = ydl.extract_info(url, download=False)
            
            # Handle playlist
            if 'entries' in info:
                videos = []
                for entry in info.get('entries', [])[:5]:  # First 5 videos
                    if entry:
                        videos.append({
                            'title': entry.get('title', 'Unknown'),
                            'duration': entry.get('duration', 0),
                            'thumbnail': entry.get('thumbnail', '')
                        })
                
                return jsonify({
                    'type': 'playlist',
                    'title': info.get('title', 'Playlist'),
                    'count': len(info.get('entries', [])),
                    'videos': videos,
                    'thumbnail': info.get('thumbnail') or (videos[0]['thumbnail'] if videos else '')
                })
            else:
                # Single video
                return jsonify({
                    'type': 'video',
                    'title': info.get('title', 'Unknown'),
                    'duration': info.get('duration', 0),
                    'thumbnail': info.get('thumbnail', ''),
                    'uploader': info.get('uploader', 'Unknown')
                })
    except Exception as e:
        return jsonify({'error': str(e)}), 500

@app.route('/download', methods=['POST'])
def download():
    """Start download process"""
    data = request.get_json()
    url = data.get('url')
    speed = float(data.get('speed', 1.0))
    
    if not url:
        return jsonify({'error': 'No URL provided'}), 400
    
    # Generate unique download ID
    download_id = str(uuid.uuid4())
    downloads[download_id] = {
        'status': 'queued',
        'message': 'Starting download...',
        'progress': '0%'
    }
    
    # Start download in background thread
    thread = threading.Thread(target=download_task, args=(url, speed, download_id))
    thread.daemon = True
    thread.start()
    
    return jsonify({'download_id': download_id})

@app.route('/status/<download_id>')
def status(download_id):
    """Get download status"""
    if download_id in downloads:
        return jsonify(downloads[download_id])
    else:
        return jsonify({'error': 'Download ID not found'}), 404

@app.route('/files')
def list_files():
    """List all MP3 files from both temp and output directories"""
    try:
        temp_files = []
        output_files = []
        
        # Get files from temp directory
        if os.path.exists(TEMP_DIR):
            for filename in os.listdir(TEMP_DIR):
                if filename.endswith('.mp3'):
                    filepath = os.path.join(TEMP_DIR, filename)
                    file_stat = os.stat(filepath)
                    file_size = file_stat.st_size
                    size_mb = round(file_size / (1024 * 1024), 2)
                    has_thumbnail = check_mp3_has_artwork(filepath)
                    
                    temp_files.append({
                        'name': filename,
                        'size': size_mb,
                        'modified': file_stat.st_mtime,
                        'has_thumbnail': has_thumbnail,
                        'location': 'temp'
                    })
        
        # Get files from output directory
        if os.path.exists(OUTPUT_DIR):
            for filename in os.listdir(OUTPUT_DIR):
                if filename.endswith('.mp3'):
                    filepath = os.path.join(OUTPUT_DIR, filename)
                    file_stat = os.stat(filepath)
                    file_size = file_stat.st_size
                    size_mb = round(file_size / (1024 * 1024), 2)
                    has_thumbnail = check_mp3_has_artwork(filepath)
                    
                    output_files.append({
                        'name': filename,
                        'size': size_mb,
                        'modified': file_stat.st_mtime,
                        'has_thumbnail': has_thumbnail,
                        'location': 'output'
                    })
        
        # Sort by modification time, newest first
        temp_files.sort(key=lambda x: x['modified'], reverse=True)
        output_files.sort(key=lambda x: x['modified'], reverse=True)
        
        return jsonify({
            'temp_files': temp_files,
            'output_files': output_files
        })
    except Exception as e:
        return jsonify({'error': str(e)}), 500

def check_mp3_has_artwork(filepath):
    """Check if MP3 file has embedded artwork"""
    try:
        result = subprocess.run(
            ['ffprobe', '-v', 'quiet', '-select_streams', 'v:0', '-show_entries', 
             'stream=codec_name', '-of', 'default=noprint_wrappers=1:nokey=1', filepath],
            capture_output=True,
            text=True,
            timeout=5
        )
        return bool(result.stdout.strip())
    except:
        return False

@app.route('/thumbnail/<location>/<path:filename>')
def get_thumbnail(location, filename):
    """Extract and serve thumbnail from MP3 file"""
    try:
        # Determine directory based on location
        if location == 'temp':
            base_dir = TEMP_DIR
        elif location == 'output':
            base_dir = OUTPUT_DIR
        else:
            return jsonify({'error': 'Invalid location'}), 400
            
        filepath = os.path.join(base_dir, filename)
        if not os.path.exists(filepath) or not filepath.startswith(base_dir):
            return jsonify({'error': 'File not found'}), 404
        
        # Extract thumbnail using ffmpeg
        result = subprocess.run(
            ['ffmpeg', '-i', filepath, '-an', '-c:v', 'copy', '-f', 'image2pipe', '-'],
            capture_output=True,
            timeout=10
        )
        
        if result.returncode == 0 and result.stdout:
            # Determine image type
            if result.stdout[:4] == b'\xff\xd8\xff\xe0' or result.stdout[:4] == b'\xff\xd8\xff\xe1':
                mimetype = 'image/jpeg'
            elif result.stdout[:8] == b'\x89PNG\r\n\x1a\n':
                mimetype = 'image/png'
            else:
                mimetype = 'image/jpeg'  # Default
            
            from flask import Response
            return Response(result.stdout, mimetype=mimetype)
        else:
            return jsonify({'error': 'No thumbnail found'}), 404
            
    except Exception as e:
        print(f"Error extracting thumbnail: {e}")
        return jsonify({'error': str(e)}), 500

@app.route('/delete/<location>/<path:filename>', methods=['DELETE'])
def delete_file(location, filename):
    """Delete a file from temp or output directory"""
    try:
        # Determine directory based on location
        if location == 'temp':
            base_dir = TEMP_DIR
        elif location == 'output':
            base_dir = OUTPUT_DIR
        else:
            return jsonify({'error': 'Invalid location'}), 400
            
        filepath = os.path.join(base_dir, filename)
        if os.path.exists(filepath) and filepath.startswith(base_dir):
            os.remove(filepath)
            return jsonify({'success': True})
        else:
            return jsonify({'error': 'File not found'}), 404
    except Exception as e:
        return jsonify({'error': str(e)}), 500

@app.route('/move', methods=['POST'])
def move_files():
    """Move selected files from temp to output directory"""
    try:
        data = request.get_json()
        filenames = data.get('filenames', [])
        
        if not filenames:
            return jsonify({'error': 'No files specified'}), 400
        
        moved_count = 0
        errors = []
        
        for filename in filenames:
            src_path = os.path.join(TEMP_DIR, filename)
            dst_path = os.path.join(OUTPUT_DIR, filename)
            
            # Security check
            if not src_path.startswith(TEMP_DIR) or not dst_path.startswith(OUTPUT_DIR):
                errors.append(f'{filename}: Invalid path')
                continue
                
            if not os.path.exists(src_path):
                errors.append(f'{filename}: File not found in temp')
                continue
            
            try:
                # Move file (shutil.move handles cross-device moves)
                shutil.move(src_path, dst_path)
                moved_count += 1
            except Exception as e:
                errors.append(f'{filename}: {str(e)}')
        
        return jsonify({
            'success': True,
            'moved': moved_count,
            'errors': errors
        })
    except Exception as e:
        return jsonify({'error': str(e)}), 500

@app.route('/stream/<location>/<path:filename>')
def stream_file(location, filename):
    """Stream audio file for playback"""
    try:
        # Determine directory based on location
        if location == 'temp':
            base_dir = TEMP_DIR
        elif location == 'output':
            base_dir = OUTPUT_DIR
        else:
            return jsonify({'error': 'Invalid location'}), 400
            
        filepath = os.path.join(base_dir, filename)
        if os.path.exists(filepath) and filepath.startswith(base_dir):
            return send_from_directory(base_dir, filename, mimetype='audio/mpeg')
        else:
            return jsonify({'error': 'File not found'}), 404
    except Exception as e:
        return jsonify({'error': str(e)}), 500

if __name__ == '__main__':
    # Suppress Flask development server warning
    cli = logging.getLogger('werkzeug')
    cli.setLevel(logging.ERROR)
    
    app.run(host='0.0.0.0', port=5000, debug=False)

