import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import App from './App'
import { applyBrandColor } from './config'
import './index.css'

// Before the first render, so the header never flashes navy then repaints.
applyBrandColor()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
