document.addEventListener('DOMContentLoaded', () => {
    const uploadForm = document.getElementById('upload-form');
    const uploadButton = uploadForm.querySelector('button');
    const transcriptDiv = document.getElementById('transcript');
    const clipForm = document.getElementById('clip-form');
    const clipResultDiv = document.getElementById('clip-result');
    const manualClippingControls = document.getElementById('manual-clipping-controls');
    const clipPreview = document.getElementById('clip-preview');
    const keyframesDisplay = document.getElementById('keyframes-display');
    const manualClipButton = document.getElementById('manual-clip-button');

    let videoFile = '';
    let keyframes = [];

    // --- Upload and Transcription ---
    uploadForm.addEventListener('submit', async (e) => {
        e.preventDefault();
        uploadButton.disabled = true;
        uploadButton.textContent = 'Transcribing...';
        transcriptDiv.innerHTML = '<p>Transcription in progress...</p>';
        clipResultDiv.innerHTML = '';
        manualClippingControls.style.display = 'none';

        const formData = new FormData(uploadForm);
        try {
            const response = await fetch('/upload', { method: 'POST', body: formData });
            if (response.ok) {
                const data = await response.json();
                videoFile = data.videoFile;
                displayTranscript(data.transcript);
            } else {
                const errorText = await response.text();
                transcriptDiv.innerHTML = `<p><strong>Error:</strong> ${errorText}</p>`;
            }
        } catch (error) {
            transcriptDiv.innerHTML = `<p><strong>Error:</strong> ${error.message}</p>`;
        } finally {
            uploadButton.disabled = false;
            uploadButton.textContent = 'Upload and Transcribe';
        }
    });

    function displayTranscript(transcript) {
        transcriptDiv.innerHTML = '';
        if (transcript && transcript.length > 0) {
            transcript.forEach(segment => {
                const p = document.createElement('p');
                p.textContent = `[${formatTime(segment.start)} - ${formatTime(segment.end)}] ${segment.text}`;
                p.dataset.start = segment.start;
                p.dataset.end = segment.end;
                p.addEventListener('click', () => {
                    p.classList.toggle('selected');
                    updateClipSelection();
                });
                transcriptDiv.appendChild(p);
            });
        } else {
            transcriptDiv.innerHTML = '<p>No speech detected.</p>';
        }
    }

    // --- Clip Selection and Preview ---
    function updateClipSelection() {
        const selected = Array.from(transcriptDiv.querySelectorAll('p.selected'));
        if (selected.length > 0) {
            selected.sort((a, b) => parseFloat(a.dataset.start) - parseFloat(b.dataset.start));
            const startTime = selected[0].dataset.start;
            const endTime = selected[selected.length - 1].dataset.end;

            document.getElementById('start').value = startTime;
            document.getElementById('end').value = endTime;

            loadClipPreview(startTime, endTime);
            manualClippingControls.style.display = 'block';
        } else {
            document.getElementById('start').value = '';
            document.getElementById('end').value = '';
            manualClippingControls.style.display = 'none';
            clipPreview.src = '';
        }
    }

    function loadClipPreview(start, end) {
        if (videoFile && start && end) {
            clipPreview.src = `/uploads/${videoFile}`;
            clipPreview.currentTime = parseFloat(start);
            keyframes = [];
            updateKeyframesDisplay();
            manualClipButton.disabled = true;
        }
    }

    // --- Keyframe Logic ---
    clipPreview.addEventListener('click', (e) => {
        const rect = clipPreview.getBoundingClientRect();
        const x = e.clientX - rect.left;
        const naturalWidth = clipPreview.videoWidth;
        const displayWidth = rect.width;

        // Calculate the x-coordinate relative to the video's actual size
        const cropX = Math.round((x / displayWidth) * naturalWidth);
        const time = clipPreview.currentTime;

        // Add or update a keyframe
        const existingKeyframeIndex = keyframes.findIndex(kf => Math.abs(kf.time - time) < 0.1); // Check if a keyframe exists around this time
        if (existingKeyframeIndex > -1) {
            keyframes[existingKeyframeIndex].cropX = cropX;
        } else {
            keyframes.push({ time, cropX });
        }

        keyframes.sort((a, b) => a.time - b.time);
        updateKeyframesDisplay();
    });

    function updateKeyframesDisplay() {
        keyframesDisplay.innerHTML = '<h4>Keyframes:</h4>';
        if (keyframes.length > 0) {
            const ol = document.createElement('ol');
            keyframes.forEach((kf, index) => {
                const li = document.createElement('li');
                li.textContent = `Time: ${kf.time.toFixed(2)}s, CropX: ${kf.cropX}`;
                const removeBtn = document.createElement('button');
                removeBtn.textContent = 'Remove';
                removeBtn.onclick = () => {
                    keyframes.splice(index, 1);
                    updateKeyframesDisplay();
                };
                li.appendChild(removeBtn);
                ol.appendChild(li);
            });
            keyframesDisplay.appendChild(ol);
        } else {
            keyframesDisplay.innerHTML += '<p>Click on the video at different times to set crop keyframes.</p>';
        }
        manualClipButton.disabled = keyframes.length === 0;
    }

    // --- Clip Generation ---
    const handleClipRequest = async (url, button, extraFormData = {}) => {
        button.disabled = true;
        button.textContent = 'Creating...';
        clipResultDiv.innerHTML = `<p>Creating clip...</p>`;

        const formData = new FormData(clipForm);
        formData.append('videoFile', videoFile);
        for (const key in extraFormData) {
            formData.append(key, extraFormData[key]);
        }

        try {
            const response = await fetch(url, { method: 'POST', body: new URLSearchParams(formData) });
            if (response.ok) {
                const clipPath = await response.text();
                clipResultDiv.innerHTML = `
                    <p>Clip created!</p>
                    <video controls width="100%"><source src="${clipPath}" type="video/mp4"></video>
                    <a href="${clipPath}" download>Download Clip</a>`;
            } else {
                const errorText = await response.text();
                clipResultDiv.innerHTML = `<p><strong>Error:</strong> ${errorText}</p>`;
            }
        } catch (error) {
            clipResultDiv.innerHTML = `<p><strong>Error:</strong> ${error.message}</p>`;
        } finally {
            button.disabled = false;
            // Restore original button text
            if(button.id === 'manual-clip-button') button.textContent = 'Generate Manual Clip';
            else if(button.id === 'autoclip-button') button.textContent = 'Auto-Clip for Shorts';
            else button.textContent = 'Create Standard Clip';
        }
    };

    clipForm.addEventListener('submit', (e) => {
        e.preventDefault();
        handleClipRequest('/clip', clipForm.querySelector('button[type="submit"]'));
    });

    document.getElementById('autoclip-button').addEventListener('click', (e) => {
        handleClipRequest('/autoclip', e.target);
    });

    manualClipButton.addEventListener('click', () => {
        if (keyframes.length < 1) {
            alert('Please set at least one keyframe.');
            return;
        }
        const extraData = {
            keyframes: JSON.stringify(keyframes),
        };
        handleClipRequest('/manual-clip', manualClipButton, extraData);
    });

    // --- Utility Functions ---
    function formatTime(seconds) {
        const date = new Date(null);
        date.setSeconds(seconds);
        return date.toISOString().substr(11, 8);
    }
});