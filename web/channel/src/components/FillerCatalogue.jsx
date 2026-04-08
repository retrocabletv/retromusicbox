import React, { useState, useEffect, useMemo } from 'react'

const ENTRIES_PER_PAGE = 4

export default function FillerCatalogue({ catalogue, phoneNumber }) {
  const [page, setPage] = useState(0)

  const rows = useMemo(() => {
    if (!catalogue) return []
    return catalogue.map((entry) => ({
      code: entry.code,
      artist: entry.artist,
      title: entry.title,
    }))
  }, [catalogue])

  const totalPages = Math.max(Math.ceil(rows.length / ENTRIES_PER_PAGE), 1)

  // Auto-advance pages
  useEffect(() => {
    if (totalPages <= 1) return
    const interval = setInterval(() => {
      setPage((p) => (p + 1) % totalPages)
    }, 5000)
    return () => clearInterval(interval)
  }, [totalPages])

  const pageRows = rows.slice(
    page * ENTRIES_PER_PAGE,
    (page + 1) * ENTRIES_PER_PAGE
  )

  return (
    <div className="filler-catalogue">
      <div className="catalogue-header">
        <h2>M U S I C&nbsp;&nbsp;&nbsp;M E N U</h2>
      </div>

      <div className="catalogue-body">
        {pageRows.map((row, i) => (
          <div key={`${page}-${i}`} className="catalogue-entry">
            <div className="catalogue-code">{row.code}</div>
            <div className="catalogue-entry-info">
              <div className="catalogue-entry-artist">{row.artist.toUpperCase()}</div>
              <div className="catalogue-entry-title">{row.title}</div>
            </div>
          </div>
        ))}
      </div>

      <div className="catalogue-footer">
        <div className="catalogue-footer-info">
          {phoneNumber ? (
            <>
              <span className="catalogue-price">50p/min</span>
              {' / '}
              <span className="catalogue-phone">{phoneNumber}</span>
            </>
          ) : (
            <span className="catalogue-price">Request your video now</span>
          )}
        </div>
        <div className="catalogue-footer-disclaimer">
          Under 18? Get Parent's Permission
        </div>
      </div>
    </div>
  )
}
