import React, { useMemo } from 'react'

export default function FillerCatalogue({ catalogue, phoneNumber }) {
  const rows = useMemo(() => {
    if (!catalogue) return []
    return catalogue.map((entry) => ({
      code: entry.code,
      artist: entry.artist,
      title: entry.title,
    }))
  }, [catalogue])

  // Duplicate rows for seamless scroll loop
  const allRows = [...rows, ...rows]

  return (
    <div className="filler-catalogue">
      <div className="catalogue-header">
        <h2>THE BOX CATALOGUE</h2>
        <div className="catalogue-columns">
          <span className="col-code">CODE</span>
          <span className="col-artist">ARTIST</span>
          <span className="col-title">TITLE</span>
        </div>
      </div>

      <div className="catalogue-scroll-container">
        <div className="catalogue-scroll" style={{
          animationDuration: `${Math.max(rows.length * 2, 30)}s`
        }}>
          {allRows.map((row, i) => (
            <div key={i} className="catalogue-row">
              <span className="col-code">{row.code}</span>
              <span className="col-artist">{row.artist}</span>
              <span className="col-title">{row.title}</span>
            </div>
          ))}
        </div>
      </div>

      <div className="catalogue-footer">
        {phoneNumber && <span>CALL {phoneNumber}</span>}
        <span className="catalogue-cta">REQUEST YOUR VIDEO NOW!</span>
      </div>
    </div>
  )
}
