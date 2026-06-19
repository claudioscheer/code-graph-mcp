import { getSession } from "../../../../../packages/auth/src/session";

export default async function DashboardPage() {
  const session = await getSession();
  return <div>{session.user.name}</div>;
}
