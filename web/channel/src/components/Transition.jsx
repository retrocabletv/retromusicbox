import React, { useState, useEffect } from 'react'

export default function Transition({ video }) {
  const [visible, setVisible] = useState(false)

  useEffect(() => {
    requestAnimationFrame(() => {
      requestAnimationFrame(() => setVisible(true))
    })
  }, [])

  return (
    <div className={`transition ${visible ? 'visible' : ''}`}>
      <div className="transition-logo">THE BOX</div>
      <div className="transition-info">
        <span className="transition-label">COMING UP</span>
        <span className="transition-code">{video.catalogue_code}</span>
        <span className="transition-title">
          "{video.title}" – {video.artist}
        </span>
      </div>
    </div>
  )
}
