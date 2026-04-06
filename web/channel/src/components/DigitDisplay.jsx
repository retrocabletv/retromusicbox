import React, { useMemo, useState, useEffect } from 'react'

export default function DigitDisplay({ video, queue, phoneNumber, positionTotal, catalogue }) {
  const [showMode, setShowMode] = useState('catalogue') // 'catalogue' | 'upnext'
  const [catalogueIndex, setCatalogueIndex] = useState(0)

  // Cycle: show 5 catalogue entries one at a time, then show up next, repeat
  useEffect(() => {
    const entries = catalogue || []
    if (entries.length === 0 && (!queue || queue.length === 0)) return

    const interval = setInterval(() => {
      setShowMode((prev) => {
        if (prev === 'upnext') {
          setCatalogueIndex((i) => (i + 1) % Math.max(entries.length, 1))
          return 'catalogue'
        }
        // Show catalogue for a few ticks, then switch to up next
        return prev
      })
    }, 4000)

    return () => clearInterval(interval)
  }, [catalogue, queue])

  // Every 5 catalogue entries, show up next
  useEffect(() => {
    if (catalogueIndex > 0 && catalogueIndex % 5 === 0 && queue && queue.length > 0) {
      setShowMode('upnext')
    }
  }, [catalogueIndex, queue])

  const catalogueEntries = catalogue || []
  const currentEntry = catalogueEntries[catalogueIndex % Math.max(catalogueEntries.length, 1)]

  const upNextContent = useMemo(() => {
    if (!queue || queue.length === 0) {
      return phoneNumber
        ? `CALL ${phoneNumber} TO REQUEST YOUR VIDEO!`
        : 'THE BOX \u2014 YOUR MUSIC, YOUR CHOICE'
    }
    return queue.map((item) => `${item.code} ${item.artist} \u2013 "${item.title}"`).join('  \u2666  ')
  }, [queue, phoneNumber])

  const marqueeText = upNextContent + '     \u2666     ' + upNextContent

  return (
    <div className="digit-display">
      <div className="digit-display-row digit-display-now">
        {video ? (
          <>
            <span className="label">NOW PLAYING:</span>{' '}
            <span className="code">{video.catalogue_code}</span>{' '}
            <span className="info">"{video.title}" \u2013 {video.artist}</span>
          </>
        ) : (
          <span className="label">THE BOX</span>
        )}
        {positionTotal > 0 && (
          <span className="queue-count">{positionTotal} in queue</span>
        )}
      </div>
      <div className="digit-display-row digit-display-next">
        {showMode === 'upnext' && queue && queue.length > 0 ? (
          <>
            <span className="label">UP NEXT:</span>
            <div className="marquee-container">
              <div className="marquee-content">
                <span>{marqueeText}</span>
              </div>
            </div>
          </>
        ) : currentEntry ? (
          <>
            <span className="label catalogue-label">{currentEntry.code}</span>
            <span className="catalogue-info">{currentEntry.artist} \u2013 "{currentEntry.title}"</span>
          </>
        ) : (
          <>
            <span className="label">UP NEXT:</span>
            <div className="marquee-container">
              <div className="marquee-content">
                <span>{marqueeText}</span>
              </div>
            </div>
          </>
        )}
      </div>
    </div>
  )
}
