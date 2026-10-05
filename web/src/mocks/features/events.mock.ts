import { FakeEvents } from '../events-socket'
import { defineMockModule } from '../registry'
export default defineMockModule('events', (owner) => {
  owner.socket('/events', ({ url }) => new FakeEvents(url.href, owner.scenarios.state.empty))
})
