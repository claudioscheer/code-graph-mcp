import { getSession } from "../../../../../../packages/auth/src/session";

export async function GET() {
  return Response.json(await getSession());
}
