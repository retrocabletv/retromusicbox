import React from 'react'

export default function FillerIdent({ phoneNumber }) {
  return (
    <div className="filler-ident">
      <div className="ident-glow" />
      <div className="ident-content">
        <div className="ident-lines">
          <div className="ident-line" />
          <div className="ident-line" />
          <div className="ident-line" />
        </div>
        <h1 className="ident-title">THE BOX</h1>
        <div className="ident-subtitle">MUSIC TELEVISION</div>
        {phoneNumber && (
          <div className="ident-phone">{phoneNumber}</div>
        )}
        <div className="ident-cta">CALL NOW TO REQUEST YOUR VIDEO!</div>
      </div>
    </div>
  )
}
