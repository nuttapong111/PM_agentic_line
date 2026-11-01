// Cards a tool wants shown with the reply (slot picker, risk list…). The agent drains them per turn.
import type { messagingApi } from "@line/bot-sdk";

const queue: messagingApi.Message[] = [];
export const attachCard = (m: messagingApi.Message) => void queue.push(m);
export const takeCards = () => queue.splice(0);
