import React, { useState, useCallback, useEffect } from 'react'
import { useBoxSocket } from './hooks/useBoxSocket'
import VideoPlayer from './components/VideoPlayer'
import DigitDisplay from './components/DigitDisplay'
import FillerIdent from './components/FillerIdent'
import FillerCatalogue from './components/FillerCatalogue'
import Transition from './components/Transition'
import Scanlines from './components/Scanlines'

export default function App() {
  const [mode, setMode] = useState('filler') // filler | playing | transition
  const [video, setVideo] = useState(null)
  const [queue, setQueue] = useState([])
  const [positionTotal, setPositionTotal] = useState(0)
  const [fillerMode, setFillerMode] = useState('ident')
  const [catalogue, setCatalogue] = useState([])
  const [phoneNumber, setPhoneNumber] = useState('')
  const [transitionVideo, setTransitionVideo] = useState(null)

  // Fetch catalogue on mount so the digit display always has entries to cycle
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
        setMode('playing')
        break

      case 'filler':
        setFillerMode(msg.mode || 'ident')
        setCatalogue(msg.catalogue || [])
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
        setFillerMode('ident')
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

  return (
    <div className="channel">
      {!connected && (
        <div className="connecting-indicator">CONNECTING...</div>
      )}

      <div className="channel-main">
        {mode === 'playing' && video && (
          <VideoPlayer
            video={video}
            onEnded={handleVideoEnded}
            onError={handleVideoError}
          />
        )}

        {mode === 'filler' && fillerMode === 'ident' && (
          <FillerIdent phoneNumber={phoneNumber} />
        )}

        {mode === 'filler' && fillerMode === 'catalogue_scroll' && (
          <FillerCatalogue catalogue={catalogue} phoneNumber={phoneNumber} />
        )}

        {mode === 'transition' && transitionVideo && (
          <Transition video={transitionVideo} />
        )}
      </div>

      <DigitDisplay
        video={mode === 'playing' ? video : null}
        queue={queue}
        phoneNumber={phoneNumber}
        positionTotal={positionTotal}
        catalogue={catalogue}
      />

      <Scanlines />
    </div>
  )
}
