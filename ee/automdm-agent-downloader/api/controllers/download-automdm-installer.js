module.exports = {


  friendlyName: 'Download automdm installer',


  description: 'Download automdm installer file (returning a stream).',


  exits: {
    success: {
      outputFriendlyName: 'AutoMDM installer',
      outputDescription: 'The streaming bytes of an AutoMDM installer',
      outputType: 'ref',
    }
  },


  fn: async function () {

    let downloading;

    downloading = await sails.startDownload(sails.config.custom.uploadedInstallerFileDescriptor, {bucket: sails.config.uploads.bucket});

    this.res.type('application/x-ms-installer');
    this.res.attachment('AutoMDM-installer.msi');

    return downloading;
  }


};
