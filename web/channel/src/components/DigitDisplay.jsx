import React, { useMemo } from 'react'

export default function DigitDisplay({ video, queue, phoneNumber, positionTotal }) {
  const marqueeContent = useMemo(() => {
    if (!queue || queue.length === 0) {
      return phoneNumber
        ? `CALL ${phoneNumber} TO REQUEST YOUR VIDEO!`
        : 'THE BOX — YOUR MUSIC, YOUR CHOICE'
    }
    return queue.map((item) => item.code).join(' \u2666 ')
  }, [queue, phoneNumber])

  const nowPlaying = video
    ? `${video.catalogue_code} "${video.title}" \u2013 ${video.artist}`
    : null

  // Double the marquee text for seamless looping
  const marqueeText = marqueeContent + '     \u2666     ' + marqueeContent

  return (
    <div className="digit-display">
      <div className="digit-display-row digit-display-now">
        {nowPlaying ? (
          <>
            <span className="label">NOW PLAYING:</span>{' '}
            <span className="code">{video.catalogue_code}</span>{' '}
            <span className="info">"{video.title}" – {video.artist}</span>
          </>
        ) : (
          <span className="label">THE BOX</span>
        )}
        {positionTotal > 0 && (
          <span className="queue-count">{positionTotal} in queue</span>
        )}
      </div>
      <div className="digit-display-row digit-display-next">
        <span className="label">UP NEXT:</span>
        <div className="marquee-container">
          <div className="marquee-content">
            <span>{marqueeText}</span>
          </div>
        </div>
      </div>
    </div>
  )
}
