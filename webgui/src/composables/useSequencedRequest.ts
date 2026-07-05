// Guards against an out-of-order response overwriting a newer one — e.g. two
// rapid debounced fetches racing over the network, where the older request's
// response arrives after the newer one's. Call next() when a request starts
// and capture its token; only apply the response if isCurrent(token) still
// holds by the time it resolves.
export function useSequencedRequest() {
  let seq = 0

  function next(): number {
    return ++seq
  }

  function isCurrent(token: number): boolean {
    return token === seq
  }

  return { next, isCurrent }
}
