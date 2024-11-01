import {
  Button,
  Card,
  CardBody,
  CardFooter,
  CardHeader,
  Divider,
  Input,
  Select,
  SelectItem,
} from "@nextui-org/react";
import React, { useEffect, useState } from "react";

/**
 * AppInfo represents a bfiola.dev/v1/Access resource as an application.
 */
interface AppInfo {
  namespace: string;
  name: string;
  dns: string;
}

/**
 * StatusInfo represents a status information displayed to a user - often a status of a remote request.
 */
interface StatusInfo {
  value: "success" | "error";
  detail: string;
}

/**
 * (Conditionally) renders a box containing status information.
 *
 * @param props the component props
 * @returns the component
 */
const Status = (props: { status: StatusInfo | null }) => {
  if (props.status === null) {
    return null;
  }

  let bgColor = "text-success-500";
  if (props.status.value === "error") {
    bgColor = "text-danger-500";
  }
  return (
    <>
      <div className={`flex gap-3 ${bgColor} px-1`}>
        <div className="flex flex-col">
          <p className="text-xs">{props.status.detail}</p>
        </div>
      </div>
    </>
  );
};

/**
 * ResponseError is a specialized Error class holding non-200 response data.
 */
class ResponseError extends Error {
  response: Response;
  __responseError: boolean;

  constructor(response: Response) {
    super(
      `Request to ${response.url} failed with status code ${response.status}`
    );
    this.response = response;
    this.__responseError = true;
  }
}

/**
 * Typeguard ensuring the provided value is of type ResponseError
 * @param v a value
 * @returns true if v is an instance of ResponseError
 */
const isResponseError = (v: any): v is ResponseError => {
  return v["__responseError"] !== undefined;
};

/**
 * Raises an error when a response is not-ok.
 * Otherwise, returns a response.
 * @param r response
 * @returns the response
 */
const raiseOnError = (r: Response) => {
  if (!r.ok) {
    throw new ResponseError(r);
  }
  return r;
};

/**
 * Makes an API call to the backend to list known applications.
 *
 * See: /api/apps/list
 * @returns a list of AppInfo objects
 */
const listApps = async () => {
  return fetch("/api/apps/list")
    .then(raiseOnError)
    .then((value) => value.json())
    .then((data: AppInfo[]) => data);
};

/**
 * Authorizes the client to use the given AppInfo.
 *
 * See: /api/apps/:namespace/:name/authorize
 *
 * @param app The app to authorize for
 * @param password The password to authorize with
 * @returns the successful backend response
 */
const authorizeApp = async (app: AppInfo, password: string) => {
  return await fetch(`/api/apps/${app.namespace}/${app.name}/authorize`, {
    method: "post",
    body: JSON.stringify({ password: password }),
    headers: { "content-type": "application/json" },
  }).then(raiseOnError);
};

function App() {
  // assemble state
  const [apps, setApps] = useState<AppInfo[]>([]);
  const [app, setApp] = useState<AppInfo | null>(null);
  const [password, setPassword] = useState<string>("");
  const [isLoading, setIsLoading] = useState<boolean>(false);
  const [status, setStatus] = useState<StatusInfo | null>(null);

  // perform initial app fetch
  useEffect(() => {
    listApps().then(setApps);
  }, []);

  //derive data
  const sorted = apps.sort((a, b) => a.dns.localeCompare(b.dns));

  // handlers
  const onAppSelect = (event: React.ChangeEvent<HTMLSelectElement>) => {
    setApp(sorted[parseInt(event.target.value)]);
  };
  const onPasswordInput = (event: React.ChangeEvent<HTMLInputElement>) => {
    setPassword(event.target.value);
  };
  const onSubmit = async () => {
    if (app === null || password === "") {
      return;
    }

    setIsLoading(true);
    setStatus(null);

    let status: StatusInfo = {
      value: "success",
      detail: "Authorization successful",
    };
    try {
      await authorizeApp(app, password);
    } catch (e) {
      status.value = "error";
      status.detail = `${e}`;
      if (isResponseError(e)) {
        if (e.response.status === 401) {
          status.detail = "Invalid password";
        }
      }
    }

    setStatus(status);
    setIsLoading(false);
  };

  return (
    <Card className="max-w-[600px]" isDisabled={isLoading}>
      <CardHeader className="flex gap-3">
        <div className="flex flex-col">
          <p className="text-md">Authorization</p>
        </div>
      </CardHeader>
      <Divider />
      <CardBody>
        <Select
          description="Application"
          label="Select an application"
          onChange={onAppSelect}
        >
          {sorted.map((a, i) => (
            <SelectItem key={i}>{a.dns}</SelectItem>
          ))}
        </Select>
        <Input description="Password" onInput={onPasswordInput}></Input>
        <Status status={status} />
      </CardBody>
      <Divider />
      <CardFooter className="flex gap-3 justify-center">
        <Button
          className="w-[300px] bg-success-500"
          onClick={onSubmit}
          isDisabled={isLoading || app === null || password === ""}
          isLoading={isLoading}
        >
          Submit
        </Button>
      </CardFooter>
    </Card>
  );
}

export default App;
