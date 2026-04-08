import React, { useState, useCallback, useEffect } from 'react'
import { useBoxSocket } from './hooks/useBoxSocket'
import VideoPlayer from './components/VideoPlayer'
import BoxLogo from './components/BoxLogo'
import NowPlaying from './components/NowPlaying'
import BottomTicker from './components/BottomTicker'
import RequestDigits from './components/RequestDigits'
import FillerCatalogue from './components/FillerCatalogue'
import Transition from './components/Transition'
import Scanlines from './components/Scanlines'

export default function App() {
  const [started, setStarted] = useState(false)
  const [mode, setMode] = useState('filler') // filler | playing | transition
  const [video, setVideo] = useState(null)
  const [queue, setQueue] = useState([])
  const [positionTotal, setPositionTotal] = useState(0)
  const [fillerMode, setFillerMode] = useState('catalogue_scroll')
  const [catalogue, setCatalogue] = useState([])
  const [phoneNumber, setPhoneNumber] = useState('')
  const [transitionVideo, setTransitionVideo] = useState(null)
  const [callers, setCallers] = useState([])

  // Fetch catalogue on mount so overlays always have entries
  useEffect(() => {
    fetch('/api/catalogue?limit=999')
      .then((r) => r.json())
      .then((data) => { if (Array.isArray(data)) setCatalogue(data) })
      .catch(() => {})
  }, [])

  const handleMessage = useCallback((msg) => {
    switch (msg.type) {
      case 'play':
        setVideo(msg.video)
        setQueue(msg.queue || [])
        setPositionTotal(msg.position_total || 0)
        if (msg.catalogue && msg.catalogue.length > 0) {
          setCatalogue(msg.catalogue)
        }
        setMode('playing')
        break

      case 'filler':
        setFillerMode(msg.mode || 'ident')
        if (msg.catalogue && msg.catalogue.length > 0) {
          setCatalogue(msg.catalogue)
        }
        setPhoneNumber(msg.phone_number || '')
        setMode('filler')
        break

      case 'queue_update':
        setQueue(msg.queue || [])
        setPositionTotal(msg.position_total || 0)
        break

      case 'transition':
        setTransitionVideo(msg.video)
        setQueue(msg.queue || [])
        setPositionTotal(msg.position_total || 0)
        setMode('transition')
        break

      case 'skip':
        setMode('filler')
        break

      // Phone request digit stream from IVR
      case 'dial_update':
        setCallers(msg.callers || [])
        break
    }
  }, [])

  const { connected, sendMessage } = useBoxSocket(handleMessage)

  const handleVideoEnded = useCallback(() => {
    sendMessage({ type: 'video_ended' })
  }, [sendMessage])

  const handleVideoError = useCallback((error) => {
    sendMessage({ type: 'video_error', error })
  }, [sendMessage])

  if (!started) {
    return (
      <div className="channel" onClick={() => setStarted(true)} style={{ cursor: 'pointer' }}>
        <div className="click-to-start">
          <div className="ident-title" style={{ fontSize: '48px' }}>THE BOX</div>
          <div style={{
            fontFamily: "'STV5730A', monospace",
            fontSize: '14px',
            fontWeight: 'bold',
            color: '#00FFFF',
            marginTop: '20px',
          }}>CLICK TO START</div>
        </div>
      </div>
    )
  }

  return (
    <div className="channel">
      {!connected && (
        <div className="connecting-indicator">CONNECTING...</div>
      )}

      {/* Layer 0: Video (full frame) */}
      {mode === 'playing' && video && (
        <VideoPlayer
          video={video}
          onEnded={handleVideoEnded}
          onError={handleVideoError}
        />
      )}

      {/* Layer 1: Content screens (filler/transition — replace video entirely) */}
      {mode !== 'playing' && (
        <div className="channel-content">
          {mode === 'filler' && (
            <FillerCatalogue catalogue={catalogue} phoneNumber={phoneNumber} />
          )}

          {mode === 'transition' && (
            <Transition />
          )}
        </div>
      )}

      {/* Layer 2: Overlays (always on top) */}
      <div className="channel-overlays">
        <BoxLogo />

        <NowPlaying video={video} mode={mode} />

        <BottomTicker
          catalogue={catalogue}
          queue={queue}
          phoneNumber={phoneNumber}
          mode={mode}
          video={video}
        />

        <RequestDigits callers={callers} />
      </div>

      {/* Layer 3: Scanlines (topmost) */}
      <Scanlines />
    </div>
  )
}
