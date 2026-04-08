import React, { useState, useEffect, useMemo, useRef } from 'react'

function toTitleCase(str) {
  return str.replace(/\w\S*/g, (txt) =>
    txt.charAt(0).toUpperCase() + txt.substr(1).toLowerCase()
  )
}

export default function BottomTicker({ catalogue, queue, phoneNumber, mode, video }) {
  const [entryIndex, setEntryIndex] = useState(0)
  const [promoIndex, setPromoIndex] = useState(0)
  const promoRef = useRef(null)
  const entryRef = useRef(null)

  const entries = useMemo(() => {
    const items = []
    if (queue) {
      queue.forEach((q) => items.push(q))
    }
    if (catalogue) {
      catalogue.forEach((c) => items.push(c))
    }
    return items
  }, [catalogue, queue])

  const promoMessages = useMemo(() => {
    const msgs = []
    if (phoneNumber) {
      msgs.push('Any Video, Any Time...Call ' + phoneNumber)
    }
    msgs.push('The Box - Your Music, Your Choice')
    msgs.push('Under 18? Get Parent\'s Permission Before Calling')
    if (phoneNumber) {
      msgs.push('Call Now To Request Your Video!')
    }
    msgs.push('The Box - You Control The Music')
    return msgs
  }, [phoneNumber])

  // Cycle through entries every 4 seconds
  useEffect(() => {
    if (entries.length === 0) return
    entryRef.current = setInterval(() => {
      setEntryIndex((i) => (i + 1) % entries.length)
    }, 4000)
    return () => clearInterval(entryRef.current)
  }, [entries.length])

  // Cycle promo messages every 5 seconds
  useEffect(() => {
    if (promoMessages.length === 0) return
    promoRef.current = setInterval(() => {
      setPromoIndex((i) => (i + 1) % promoMessages.length)
    }, 5000)
    return () => clearInterval(promoRef.current)
  }, [promoMessages.length])

  // Don't render during filler screens or ad breaks
  if (mode !== 'playing') return null
  if (video?.is_ad) return null

  const currentEntry = entries.length > 0 ? entries[entryIndex % entries.length] : null
  const currentPromo = promoMessages[promoIndex % promoMessages.length]

  return (
    <div className="bottom-ticker">
      <div className="ticker-line ticker-line-selection">
        {currentEntry && (
          <>
            <span className="ticker-code">{currentEntry.code}</span>
            <span className="ticker-sep">{' \u2022 '}</span>
            <span className="ticker-artist">{(currentEntry.artist || '').toUpperCase()}</span>
            <span className="ticker-sep">{' \u2022 '}</span>
            <span className="ticker-title">{toTitleCase(currentEntry.title || '')}</span>
          </>
        )}
      </div>
      <div className="ticker-line ticker-line-promo">
        <span className="ticker-promo">{currentPromo}</span>
      </div>
    </div>
  )
}
