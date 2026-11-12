import { createFileRoute } from "@tanstack/react-router"

import { InstructionsPage } from "@/components/instructions/instructions-page"

export const Route = createFileRoute("/instructions")({
  component: InstructionsPage,
})
