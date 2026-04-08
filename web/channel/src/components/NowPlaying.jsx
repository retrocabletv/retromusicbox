import React, { useState, useEffect, useRef } from 'react'

export default function NowPlaying({ video, mode }) {
  const [visible, setVisible] = useState(false)
  const timerRef = useRef(null)
  const videoRef = useRef(null)

  useEffect(() => {
    if (timerRef.current) clearTimeout(timerRef.current)

    if (mode === 'playing' && video && !video.is_ad) {
      // Show caption at start of video
      setVisible(true)
      videoRef.current = video.catalogue_code

      // Hide after 6 seconds
      timerRef.current = setTimeout(() => setVisible(false), 6000)
    } else {
      setVisible(false)
    }

    return () => {
      if (timerRef.current) clearTimeout(timerRef.current)
    }
  }, [video?.catalogue_code, mode])

  if (mode !== 'playing' || !video || video.is_ad) return null

  return (
    <>
      {/* Now Playing caption — top-left */}
      <div className={`now-playing ${visible ? 'visible' : ''}`}>
        <div className="now-playing-artist">{video.artist}</div>
        <div className="now-playing-title">{video.title}</div>
        {video.label && (
          <div className="now-playing-label">{video.label}</div>
        )}
      </div>

      {/* Code badge — top-right next to logo */}
      <div className={`code-badge ${visible ? 'visible' : ''}`}>
        <span className="code-badge-arrow">&#x2192;</span>
        <span className="code-badge-number">{video.catalogue_code}</span>
      </div>
    </>
  )
}
