# Use Python 3.11 slim image
FROM python:3.11-slim

# Install ffmpeg, Node.js and npm
RUN apt-get update && \
    apt-get install -y ffmpeg curl && \
    curl -fsSL https://deb.nodesource.com/setup_20.x | bash - && \
    apt-get install -y nodejs && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

# Set working directory
WORKDIR /app

# Copy requirements first for better caching
COPY requirements.txt .

# Install Python dependencies
RUN pip install --no-cache-dir -r requirements.txt

# Copy package files for npm
COPY package.json tailwind.config.js ./

# Install npm dependencies
RUN npm install

# Copy application files
COPY app.py .
COPY templates/ templates/
COPY static/ static/

# Build Tailwind CSS
RUN npm run build:css

# Create temp and output directories
RUN mkdir -p /temp /output

# Expose port 5000
EXPOSE 5000

# Run the application
CMD ["python", "app.py"]

