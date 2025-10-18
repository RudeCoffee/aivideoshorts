document.addEventListener('DOMContentLoaded', () => {
    const uploadForm = document.getElementById('upload-form');
    const uploadButton = uploadForm.querySelector('button');
    const transcriptDiv = document.getElementById('transcript');
    const clipForm = document.getElementById('clip-form');
    const clipResultDiv = document.getElementById('clip-result');
    const cropContainer = document.getElementById('crop-container');
    const firstFrameImg = document.getElementById('first-frame');
    const cropMarker = document.getElementById('crop-marker');
    let videoFile = '';
    let cropX = -1;

    uploadForm.addEventListener('submit', async (e) => {
        e.preventDefault();
        uploadButton.disabled = true;
        uploadButton.textContent = 'Transcribing...';
        transcriptDiv.innerHTML = '<p>Transcription in progress...</p>';
        clipResultDiv.innerHTML = '';
        cropContainer.style.display = 'none';

        const formData = new FormData(uploadForm);
        try {
            const response = await fetch('/upload', { method: 'POST', body: formData });
            if (response.ok) {
                const data = await response.json();
                videoFile = data.videoFile;
                transcriptDiv.innerHTML = '';
                if (data.transcript && data.transcript.length > 0) {
                    data.transcript.forEach(segment => {
                        const p = document.createElement('p');
                        p.textContent = segment.text;
                        p.dataset.start = segment.start;
                        p.dataset.end = segment.end;
                        p.addEventListener('click', () => {
                            p.classList.toggle('selected');
                            updateClipTimes();
                        });
                        transcriptDiv.appendChild(p);
                    });
                } else {
                    transcriptDiv.innerHTML = '<p>No speech detected.</p>';
                }
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
                cropContainer.style.display = 'none';
            } else {
                const errorText = await response.text();
                clipResultDiv.innerHTML = `<p><strong>Error:</strong> ${errorText}</p>`;
            }
        } catch (error) {
            clipResultDiv.innerHTML = `<p><strong>Error:</strong> ${error.message}</p>`;
        } finally {
            button.disabled = false;
            button.textContent = button.id === 'autoclip-button' ? 'Auto-Clip for Shorts' : 'Create Clip';
        }
    };

    clipForm.addEventListener('submit', (e) => {
        e.preventDefault();
        handleClipRequest('/clip', clipForm.querySelector('button[type="submit"]'));
    });

    const startInput = document.getElementById('start');
    const endInput = document.getElementById('end');

    async function fetchAndShowFirstFrame() {
        const start = startInput.value;
        const end = endInput.value;

        if (videoFile && start && end && parseFloat(start) < parseFloat(end)) {
            try {
                const response = await fetch(`/first-frame?videoFile=${videoFile}&start=${start}`);
                if (response.ok) {
                    const framePath = await response.text();
                    firstFrameImg.src = framePath + `?t=${new Date().getTime()}`; // bust cache
                    cropContainer.style.display = 'block';
                    cropMarker.style.display = 'none';
                    cropX = -1;
                } else {
                    console.error('Failed to fetch first frame');
                    cropContainer.style.display = 'none';
                }
            } catch (error) {
                console.error('Error fetching first frame:', error);
                cropContainer.style.display = 'none';
            }
        } else {
            cropContainer.style.display = 'none';
        }
    }

    function updateClipTimes() {
        const selected = Array.from(transcriptDiv.querySelectorAll('p.selected'));
        if (selected.length > 0) {
            selected.sort((a, b) => a.dataset.start - b.dataset.start);
            const minStart = selected[0].dataset.start;
            const maxEnd = selected[selected.length - 1].dataset.end;
            startInput.value = minStart;
            endInput.value = maxEnd;
        } else {
            startInput.value = '';
            endInput.value = '';
        }
    }

    document.getElementById('get-frame-button').addEventListener('click', fetchAndShowFirstFrame);

    firstFrameImg.addEventListener('click', (e) => {
        const rect = firstFrameImg.getBoundingClientRect();
        const x = e.clientX - rect.left;
        const y = e.clientY - rect.top;

        const naturalWidth = firstFrameImg.naturalWidth;
        const displayWidth = rect.width;

        cropX = Math.round((x / displayWidth) * naturalWidth);

        cropMarker.style.left = `${x}px`;
        cropMarker.style.top = `${y}px`;
        cropMarker.style.display = 'block';
    });

    document.getElementById('autoclip-button').addEventListener('click', () => {
        const extraData = {};
        if (cropX !== -1) {
            extraData.cropX = cropX;
        }
        handleClipRequest('/autoclip', document.getElementById('autoclip-button'), extraData);
    });
});