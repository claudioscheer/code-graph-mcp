export type User = {
  id: string;
  name: string;
};

export interface Session {
  user: User;
}

export const SESSION_TTL_MS = 300000;

export const normalizeUser = (user: User): User => ({
  id: user.id,
  name: user.name.trim(),
});

export async function getSession(): Promise<Session> {
  if (!process.env.AUTH_SECRET) {
    throw new Error("missing secret");
  }

  return {
    user: normalizeUser({
      id: "1",
      name: "Ada",
    }),
  };
}
