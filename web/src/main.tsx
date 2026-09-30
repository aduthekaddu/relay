import { render } from 'preact'
import { App } from './app/App'
import { registerAll } from './command/all'
import './styles/index.css'

registerAll()
render(<App />, document.getElementById('app')!)
