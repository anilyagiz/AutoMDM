import { AxiosError, isAxiosError } from "axios";

/**
 * IAutoMDMApiError is the shape of an AutoMDM API error. It represents an element of the `errors`
 * array in an AutoMDM API response for failed requests (see `IAutoMDMApiResponseWithErrors`).
 */
export interface IAutoMDMApiError {
  name: string;
  reason: string;
}

/**
 * IApiError is the shape of an AutoMDM API response for failed requests.
 *
 * TODO: Rename to IAutoMDMApiResponseWithErrors
 */
export interface IApiError {
  message: string;
  errors: IAutoMDMApiError[];
  uuid?: string;
}

export const isAutoMDMApiError = (err: unknown): err is IAutoMDMApiError => {
  if (!err || typeof err !== "object" || !("name" in err && "reason" in err)) {
    return false;
  }
  const e = err as Record<"name" | "reason", unknown>;
  if (typeof e.name !== "string" || typeof e.reason !== "string") {
    return false;
  }
  return true;
};

interface IRecordWithErrors extends Record<string | number | symbol, unknown> {
  errors: unknown[];
}

const isRecordWithErrors = (r: unknown): r is IRecordWithErrors => {
  if (!r || typeof r !== "object" || !("errors" in r)) {
    return false;
  }
  const { errors } = r as { errors: unknown };
  if (!Array.isArray(errors)) {
    return false;
  }
  return true;
};

interface IRecordWithDataErrors
  extends Record<string | number | symbol, unknown> {
  data: IRecordWithErrors;
}

const isRecordWithDataErrors = (r: unknown): r is IRecordWithDataErrors => {
  if (!r || typeof r !== "object" || !("data" in r)) {
    return false;
  }
  const { data } = r as { data: unknown };
  if (!isRecordWithErrors(data)) {
    return false;
  }
  const { errors } = data;
  if (!Array.isArray(errors)) {
    return false;
  }
  return true;
};

interface IRecordWithResponseDataErrors
  extends Record<string | number | symbol, unknown> {
  response: IRecordWithDataErrors;
}

const isRecordWithResponseDataErrors = (
  r: unknown
): r is IRecordWithResponseDataErrors => {
  if (!r || typeof r !== "object" || !("response" in r)) {
    return false;
  }
  const { response } = r as { response: unknown };
  if (!isRecordWithDataErrors(response)) {
    return false;
  }
  return true;
};

interface IFilterAutoMDMErrorBase {
  nameEquals?: string;
  reasonIncludes?: string;
}

interface IFilterAutoMDMErrorName extends IFilterAutoMDMErrorBase {
  nameEquals: string;
  reasonIncludes?: never;
}

interface IFilterAutoMDMErrorReason extends IFilterAutoMDMErrorBase {
  nameEquals?: never;
  reasonIncludes: string;
}

// FilterAutoMDMError is the shape of a filter that can be applied to to filter AutoMDM
// server errors. It is the union of FilterAutoMDMErrorName and FilterAutoMDMErrorReason,
// which ensures that only one of `nameEquals` or `reasonIncludes` can be specified.
type IFilterAutoMDMError = IFilterAutoMDMErrorName | IFilterAutoMDMErrorReason;

const filterAutoMDMErrorNameEquals = (errs: unknown[], value: string) => {
  if (!value || !errs?.length) {
    return undefined;
  }
  return errs?.find((e) => isAutoMDMApiError(e) && e.name === value) as
    | IAutoMDMApiError
    | undefined;
};

const filterAutoMDMErrorReasonIncludes = (errs: unknown[], value: string) => {
  if (!value || !errs?.length) {
    return undefined;
  }
  return errs?.find((e) => isAutoMDMApiError(e) && e.reason?.includes(value)) as
    | IAutoMDMApiError
    | undefined;
};

const getReasonFromErrors = (errors: unknown[], filter?: IFilterAutoMDMError) => {
  if (!errors.length) {
    return "";
  }

  let automdmError: IAutoMDMApiError | undefined;
  if (filter?.nameEquals) {
    automdmError = filterAutoMDMErrorNameEquals(errors, filter.nameEquals);
  } else if (filter?.reasonIncludes) {
    automdmError = filterAutoMDMErrorReasonIncludes(errors, filter.reasonIncludes);
  } else {
    automdmError = isAutoMDMApiError(errors[0]) ? errors[0] : undefined;
  }

  return automdmError?.reason || "";
};

const getReasonFromRecordWithDataErrors = (
  r: IRecordWithDataErrors,
  filter?: IFilterAutoMDMError
): string => {
  return getReasonFromErrors(r.data.errors, filter);
};

const getReasonFromAxiosError = (
  ae: AxiosError,
  filter?: IFilterAutoMDMError
): string => {
  return isRecordWithDataErrors(ae.response)
    ? getReasonFromRecordWithDataErrors(ae.response, filter)
    : "";
};

/**
 * getErrorReason attempts to parse a unknown payload as an `AxiosError` or
 * other `Record`-like object with the general shape as follows:
 * `{ response: { data: { errors: unknown[] } } }`
 *
 * It attempts to extract a `reason` from an AutoMDM API error (i.e. an object
 * with `name` and `reason` properties) in the `errors` array, if present.
 * Other in values in the payload are generally ignored.
 *
 * If `filter` is specified, it attempts to find an error that satisfies the filter
 * and returns the `reason`, if found. Otherwise, it returns the `reason`
 * of the first error, if any.
 *
 * By default, an empty string is returned as the reason if no error is found.
 */
export const getErrorReason = (
  payload: unknown | undefined,
  filter?: IFilterAutoMDMError
): string => {
  if (isAxiosError(payload)) {
    return getReasonFromAxiosError(payload, filter);
  }

  if (isRecordWithResponseDataErrors(payload)) {
    return getReasonFromRecordWithDataErrors(payload.response, filter);
  }

  if (isRecordWithDataErrors(payload)) {
    return getReasonFromRecordWithDataErrors(payload, filter);
  }

  if (isRecordWithErrors(payload)) {
    return getReasonFromErrors(payload.errors, filter);
  }

  return "";
};

// getAutoMDMApiErrors returns the AutoMDM API errors in a payload of any shape
// getErrorReason accepts.
const getAutoMDMApiErrors = (payload: unknown): IAutoMDMApiError[] => {
  let errors: unknown[] = [];
  if (isAxiosError(payload)) {
    errors = isRecordWithDataErrors(payload.response)
      ? payload.response.data.errors
      : [];
  } else if (isRecordWithResponseDataErrors(payload)) {
    errors = payload.response.data.errors;
  } else if (isRecordWithDataErrors(payload)) {
    errors = payload.data.errors;
  } else if (isRecordWithErrors(payload)) {
    errors = payload.errors;
  }
  return errors.filter(isAutoMDMApiError);
};

/**
 * splitServerErrors sorts the AutoMDM API errors in a payload into the ones that
 * belong to a form field and the reasons of the rest. `nameMap` maps each
 * field key to the API error `name` reported for it. A field takes the first
 * matching error; errors with an empty reason are ignored.
 */
export const splitServerErrors = <K extends string>(
  payload: unknown,
  nameMap: Record<K, string>
): { fieldErrors: Partial<Record<K, string>>; otherReasons: string[] } => {
  const fieldByName = new Map<string, K>();
  (Object.keys(nameMap) as K[]).forEach((field) => {
    fieldByName.set(nameMap[field], field);
  });

  const fieldErrors: Partial<Record<K, string>> = {};
  const otherReasons: string[] = [];
  getAutoMDMApiErrors(payload).forEach(({ name, reason }) => {
    if (!reason) {
      return;
    }
    const field = fieldByName.get(name);
    if (field === undefined) {
      otherReasons.push(reason);
    } else if (fieldErrors[field] === undefined) {
      fieldErrors[field] = reason;
    }
  });
  return { fieldErrors, otherReasons };
};

export const hasStatusKey = (value: unknown): value is { status: number } => {
  return (
    typeof value === "object" &&
    value !== null &&
    "status" in value &&
    typeof (value as Record<string, unknown>).status === "number"
  );
};

export const ignoreAxiosError = (err: AxiosError, ignoreStatuses: number[]) => {
  // TODO - isAxiosError currently not recognizing axios error, fix
  // if (!isAxiosError(err)) {
  //   return false;
  // }
  // return !!err.response && ignoreStatuses.includes(err.response.status);
  return !!err.status && ignoreStatuses.includes(err.status);
};

/**
 * expandErrorReasonRequired attempts to expand the error reason for a required
 * field error. It looks for an AutoMDM API error with a `reason` of `"required"`
 * in the `errors` array of the payload. If found, it returns the `name` of the
 * error with the string `"required"` appended. Otherwise, it returns the
 * error reason as is.
 */
export const expandErrorReasonRequired = (err: unknown) => {
  if (isRecordWithDataErrors(err)) {
    const found = err.data.errors.find(
      (e) => isAutoMDMApiError(e) && e.reason === "required"
    );
    if (found) {
      return `${(found as IAutoMDMApiError).name} required`;
    }
  }
  return getErrorReason(err);
};
