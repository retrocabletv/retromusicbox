import React, { useState, useCallback, useEffect, useMemo } from 'react'
import { useChannelSocket } from './hooks/useChannelSocket'
import VideoPlayer from './components/VideoPlayer'
import ChannelLogo from './components/ChannelLogo'
import NowPlaying from './components/NowPlaying'
import BottomTicker from './components/BottomTicker'
import RequestDigits from './components/RequestDigits'
import FillerCatalogue from './components/FillerCatalogue'
import Transition from './components/Transition'
import Scanlines from './components/Scanlines'

export default function App() {
  // ?autoplay=1 skips the click-to-start gate — used by headless streamers
  // (webpagestreamer, etc.) that can't satisfy browser gesture requirements.
  // Direct human visitors still click once so audio autoplay isn't blocked.
  const [started, setStarted] = useState(() =>
    new URLSearchParams(window.location.search).get('autoplay') === '1'
  )
  const [mode, setMode] = useState('filler') // filler | playing | transition
  const [video, setVideo] = useState(null)
  const [queue, setQueue] = useState([])
  const [positionTotal, setPositionTotal] = useState(0)
  const [fillerMode, setFillerMode] = useState('catalogue_scroll')
  const [catalogue, setCatalogue] = useState([])
  const [phoneNumber, setPhoneNumber] = useState('')
  const [transitionVideo, setTransitionVideo] = useState(null)
  const [callers, setCallers] = useState([])

  // Catalogue rotates alphabetically by artist (then title) on screen, even
  // though the backend stores it by code. Matches the original Box preview.
  const sortedCatalogue = useMemo(() => {
    const collator = new Intl.Collator('en', { sensitivity: 'base', numeric: true })
    return [...catalogue].sort((a, b) => {
      const byArtist = collator.compare(a.artist || '', b.artist || '')
      if (byArtist !== 0) return byArtist
      return collator.compare(a.title || '', b.title || '')
    })
  }, [catalogue])

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

  const { connected, sendMessage } = useChannelSocket(handleMessage)

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
          <div className="ident-title" style={{ fontSize: '48px' }}>RETROMUSICBOX</div>
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
            <FillerCatalogue catalogue={sortedCatalogue} phoneNumber={phoneNumber} />
          )}

          {mode === 'transition' && (
            <Transition />
          )}
        </div>
      )}

      {/* Layer 2: Overlays (always on top) */}
      <div className="channel-overlays">
        <ChannelLogo />

        <NowPlaying video={video} mode={mode} />

        {callers.length === 0 ? (
          <BottomTicker
            catalogue={sortedCatalogue}
            queue={queue}
            phoneNumber={phoneNumber}
            mode={mode}
            video={video}
          />
        ) : (
          <RequestDigits callers={callers} />
        )}
      </div>

      {/* Layer 3: Scanlines (topmost) */}
      <Scanlines />
    </div>
  )
}
