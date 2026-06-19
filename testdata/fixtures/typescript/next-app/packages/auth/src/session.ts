export type User = {
  id: string;
  name: string;
};

export interface Session {
  user: User;
}

export async function getSession(): Promise<Session> {
  if (!process.env.AUTH_SECRET) {
    throw new Error("missing secret");
  }

  return {
    user: {
      id: "1",
      name: "Ada",
    },
  };
}
