import { getSession, SESSION_TTL_MS } from "../../../../../packages/auth/src/session";

export default async function DashboardPage() {
  const session = await getSession();
  const fallback = await getSession();
  return <div data-ttl={SESSION_TTL_MS}>{session.user.name || fallback.user.name}</div>;
}
