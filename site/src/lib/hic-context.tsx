import { createContext, useContext } from "react"

export type HicState = {
  browserHasHic: boolean
  preferHic: boolean
  useHic: boolean
}

const HicContext = createContext<HicState>({
  browserHasHic: false,
  preferHic: true,
  useHic: false,
})

export const HicProvider = HicContext.Provider

export function useHic() {
  return useContext(HicContext)
}
