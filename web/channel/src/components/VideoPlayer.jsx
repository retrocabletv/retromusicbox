import React, { useRef, useEffect, useState } from 'react'

export default function VideoPlayer({ video, onEnded, onError }) {
  const videoRef = useRef(null)
  const [opacity, setOpacity] = useState(0)

  useEffect(() => {
    const el = videoRef.current
    if (!el || !video?.media_url) return

    el.src = video.media_url
    el.load()

    const playPromise = el.play()
    if (playPromise) {
      playPromise.catch((err) => {
        console.error('Autoplay failed:', err)
        onError?.('Autoplay failed: ' + err.message)
      })
    }

    // Fade in
    setOpacity(0)
    requestAnimationFrame(() => {
      requestAnimationFrame(() => setOpacity(1))
    })
  }, [video?.media_url])

  return (
    <div className="video-player" style={{ opacity, transition: 'opacity 0.5s ease' }}>
      <video
        ref={videoRef}
        className="video-element"
        autoPlay
        playsInline
        onEnded={onEnded}
        onError={() => onError?.('Failed to load video')}
      />
    </div>
  )
}
