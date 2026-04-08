import React, { useState, useEffect } from 'react'

export default function Transition() {
  const [visible, setVisible] = useState(false)

  useEffect(() => {
    requestAnimationFrame(() => {
      requestAnimationFrame(() => setVisible(true))
    })
  }, [])

  return (
    <div className={`transition ${visible ? 'visible' : ''}`} />
  )
}
