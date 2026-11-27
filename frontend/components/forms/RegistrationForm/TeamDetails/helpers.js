import { size } from "lodash";

import { isValidURL } from "components/forms/validators";
import INVALID_SERVER_URL_MESSAGE from "utilities/error_messages";

const validate = (formData) => {
  const errors = {};
  const { server_url: automdmWebAddress } = formData;

  if (!automdmWebAddress) {
    errors.server_url = "AutoMDM web address must be completed";
  } else if (
    !isValidURL({
      url: automdmWebAddress,
      protocols: ["http", "https"],
      allowLocalHost: true,
    })
  ) {
    errors.server_url = INVALID_SERVER_URL_MESSAGE;
  }

  const valid = !size(errors);

  return { valid, errors };
};

export default { validate };
